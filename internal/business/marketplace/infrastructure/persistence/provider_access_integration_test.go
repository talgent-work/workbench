//go:build integration

package persistence

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	marketplacepublicv1 "github.com/talgent-work/workbench/gen/go/talgent/marketplace/public/v1"
	"github.com/talgent-work/workbench/gen/go/talgent/marketplace/public/v1/marketplacepublicv1connect"
	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/application/scopemaintenance"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
	scmgithub "github.com/talgent-work/workbench/internal/business/marketplace/infrastructure/github"
	transport "github.com/talgent-work/workbench/internal/business/marketplace/transport/connectrpc"
	marketplacehttp "github.com/talgent-work/workbench/internal/business/marketplace/transport/http"
	"github.com/talgent-work/workbench/internal/pkg/authn"
	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"
	"xorm.io/xorm"
)

// fakeGitHub serves the user-token installation APIs. Its installations can
// change, and it counts every request.
type fakeGitHub struct {
	mu            sync.Mutex
	installations map[int][]string // installation ID -> repository names
	requests      int
}

func (g *fakeGitHub) set(installations map[int][]string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.installations = installations
}

func (g *fakeGitHub) requestCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.requests
}

func (g *fakeGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.requests++
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/user/installations" {
		var list []string
		for id := 1; id <= 20; id++ {
			if _, ok := g.installations[id]; ok {
				list = append(list, fmt.Sprintf(`{"id":%d,"account":{"login":"org-%d","type":"Organization"},"repository_selection":"selected","permissions":{"contents":"write"}}`, id, id))
			}
		}
		_, _ = w.Write([]byte(`{"installations":[` + strings.Join(list, ",") + `]}`))
		return
	}
	var id int
	if _, err := fmt.Sscanf(r.URL.Path, "/user/installations/%d/repositories", &id); err != nil {
		http.Error(w, "unexpected", http.StatusNotFound)
		return
	}
	repositories, ok := g.installations[id]
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	var list []string
	for index, name := range repositories {
		list = append(list, fmt.Sprintf(`{"id":%d,"full_name":%q,"clone_url":"https://github.com/%s.git","default_branch":"main"}`, id*100+index, name, name))
	}
	_, _ = w.Write([]byte(`{"repositories":[` + strings.Join(list, ",") + `]}`))
}

// syncObservations observes at once instead of through the task queue.
type syncObservations struct {
	connections *application.PersonalConnections
}

func (s syncObservations) ScheduleObservation(ctx context.Context, authorizationID string) error {
	return s.connections.ObserveAccessScope(ctx, authorizationID)
}

type providerAccessFixture struct {
	github    *fakeGitHub
	client    marketplacepublicv1connect.MarketplaceServiceClient
	webhook   http.Handler
	reconcile func(now time.Time) error
	owner     string
}

func newProviderAccessFixture(t *testing.T) *providerAccessFixture {
	t.Helper()
	ctx := t.Context()
	db, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	github := &fakeGitHub{}
	githubServer := httptest.NewServer(github)
	t.Cleanup(githubServer.Close)

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	cfg := scmgithub.Config{
		Enabled: "true", AppID: "123", AppSlug: "talgent", OAuthClientID: "client-id", OAuthClientSecret: "client-secret",
		PrivateKeyPEM:  string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})),
		TokenCipherKey: "integration cipher key", APIBaseURL: githubServer.URL, WebBaseURL: "https://github.test",
	}
	cipher, err := scmgithub.NewTokenCipher(cfg)
	require.NoError(t, err)
	repo := NewProviderAuthorizations(db)
	token, err := cipher.Encrypt("gho_user_token")
	require.NoError(t, err)
	owner := "e6c7e756-007d-5236-a492-7f4b9be0b2e8"
	authorization, err := domain.NewProviderAuthorization(domain.ProviderAuthorizationInput{
		OwnerUserID: owner, Provider: domain.ProviderGitHub, ProviderAccountID: "12345", ProviderLogin: "octocat",
		AccessTokenCiphertext: token, ExpiresAt: time.Now().Add(8 * time.Hour),
	})
	require.NoError(t, err)
	require.NoError(t, repo.Save(ctx, authorization))

	observer, err := scmgithub.NewCatalog(cfg, repo, cipher)
	require.NoError(t, err)
	queries := NewAuthorizationMaintenanceQueries(db)
	scheduler := &syncObservations{}
	connections := application.NewPersonalConnections(repo, observer, scheduler, nil, nil, nil, repo, nil, queries)
	scheduler.connections = connections
	app := application.NewApplication(nil, nil, nil, nil, connections, application.ConnectorRepositories{})
	path, handler := marketplacepublicv1connect.NewMarketplaceServiceHandler(transport.NewHandler(app), connect.WithInterceptors(authn.NewConnectUnaryInterceptor(authn.GatewayActorConfig{Enabled: true})))
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	webhook, err := marketplacehttp.NewWebhookHandler("webhook-secret", app, slog.Default())
	require.NoError(t, err)
	return &providerAccessFixture{
		github: github, owner: owner, webhook: webhook,
		client: marketplacepublicv1connect.NewMarketplaceServiceClient(server.Client(), server.URL),
		reconcile: func(now time.Time) error {
			return scopemaintenance.NewReconcileScanProcessor(queries, scheduler, func() time.Time { return now }).Process(ctx)
		},
	}
}

func (f *providerAccessFixture) access(t *testing.T) *marketplacepublicv1.GetProviderAccessResponse {
	t.Helper()
	response, err := f.client.GetProviderAccess(t.Context(), personalRequest(&marketplacepublicv1.GetProviderAccessRequest{Provider: "github"}, f.owner))
	require.NoError(t, err)
	return response.Msg
}

func (f *providerAccessFixture) refresh(t *testing.T) *marketplacepublicv1.GetProviderAccessResponse {
	t.Helper()
	response, err := f.client.RefreshProviderAccess(t.Context(), personalRequest(&marketplacepublicv1.RefreshProviderAccessRequest{Provider: "github"}, f.owner))
	require.NoError(t, err)
	return response.Msg.Access
}

func (f *providerAccessFixture) repositories(t *testing.T) []string {
	t.Helper()
	response, err := f.client.ListAccessibleRepositories(t.Context(), personalRequest(&marketplacepublicv1.ListAccessibleRepositoriesRequest{Provider: "github"}, f.owner))
	require.NoError(t, err)
	return repositoryNames(response.Msg.Repositories)
}

func (f *providerAccessFixture) deliverWebhook(t *testing.T, event, body string) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, marketplacehttp.GitHubWebhookPath, bytes.NewReader([]byte(body)))
	request.Header.Set("X-GitHub-Event", event)
	request.Header.Set("X-GitHub-Delivery", "delivery-"+event)
	mac := hmac.New(sha256.New, []byte("webhook-secret"))
	_, _ = mac.Write([]byte(body))
	request.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	response := httptest.NewRecorder()
	f.webhook.ServeHTTP(response, request)
	require.Equal(t, http.StatusNoContent, response.Code, response.Body.String())
}

// Intent (real): the Provider page and repository choices read the retained
// scope without GitHub requests; reauthorization-independent sources —
// refresh, installation webhooks and the daily reconciliation — keep it current;
// a failed observation keeps the last scope and its time.
func TestProviderAccessIsMaintainedByChangeAndReadWithoutGitHub(t *testing.T) {
	f := newProviderAccessFixture(t)
	f.github.set(map[int][]string{1: {"org-1/api"}, 2: {"org-2/web", "org-2/docs"}})

	// Reads before any observation ask GitHub nothing.
	before := f.access(t)
	require.Equal(t, "active", before.State.Status)
	require.Empty(t, before.Accounts)
	require.Zero(t, before.ObservedAtUnixMs)
	require.Empty(t, f.repositories(t))
	require.Zero(t, f.github.requestCount(), "reads never ask GitHub")

	// An explicit refresh observes every installation.
	refreshed := f.refresh(t)
	require.False(t, refreshed.AccessUnavailable)
	require.Len(t, refreshed.Accounts, 2)
	require.NotZero(t, refreshed.ObservedAtUnixMs)
	require.Equal(t, []string{"org-1/api", "org-2/docs", "org-2/web"}, f.repositories(t))
	requests := f.github.requestCount()
	f.access(t)
	f.repositories(t)
	require.Equal(t, requests, f.github.requestCount(), "reads after observing still ask GitHub nothing")

	// installation_repositories: a repository added to installation 2.
	f.github.set(map[int][]string{1: {"org-1/api"}, 2: {"org-2/web", "org-2/docs", "org-2/new"}})
	f.deliverWebhook(t, "installation_repositories", `{"action":"added","installation":{"id":2},"sender":{"id":999}}`)
	require.Contains(t, f.repositories(t), "org-2/new")

	// installation: installation 1 removed.
	f.github.set(map[int][]string{2: {"org-2/web", "org-2/docs", "org-2/new"}})
	f.deliverWebhook(t, "installation", `{"action":"deleted","installation":{"id":1},"sender":{"id":999}}`)
	require.NotContains(t, f.repositories(t), "org-1/api")
	require.Equal(t, "active", f.access(t).State.Status, "an installation change never changes the authorization")

	// The daily reconciliation corrects a change whose notification was lost,
	// and leaves scopes observed within the day alone.
	f.github.set(map[int][]string{3: {"org-3/lost-webhook"}})
	requests = f.github.requestCount()
	require.NoError(t, f.reconcile(time.Now()))
	require.Equal(t, requests, f.github.requestCount(), "a scope observed within a day is not reconciled")
	require.NoError(t, f.reconcile(time.Now().Add(25*time.Hour)))
	require.Equal(t, []string{"org-3/lost-webhook"}, f.repositories(t))

}

// Intent (real): another user never sees the owner's scope, and what an
// unconnected user is offered is what connecting would request.
func TestProviderAccessIsTheCallersOwn(t *testing.T) {
	f := newProviderAccessFixture(t)
	f.github.set(map[int][]string{1: {"org-1/api"}})
	f.refresh(t)
	other := "cfe58e71-efc6-5336-8009-449ad568c42b"
	unconnected, err := f.client.GetProviderAccess(t.Context(), personalRequest(&marketplacepublicv1.GetProviderAccessRequest{Provider: "github"}, other))
	require.NoError(t, err)
	require.Equal(t, "none", unconnected.Msg.State.Status)
	require.Empty(t, unconnected.Msg.Accounts, "another user never sees the owner's installations")
	_, err = f.client.GetProviderAccess(t.Context(), connect.NewRequest(&marketplacepublicv1.GetProviderAccessRequest{Provider: "github"}))
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	_, err = f.client.GetProviderAccess(t.Context(), personalRequest(&marketplacepublicv1.GetProviderAccessRequest{Provider: "linear"}, f.owner))
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err), "only supported Providers are described")
}

func repositoryNames(repositories []*marketplacepublicv1.AccessibleRepository) []string {
	names := make([]string, 0, len(repositories))
	for _, repository := range repositories {
		names = append(names, repository.Name)
	}
	return names
}
