//go:build integration

package account_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/lestrrat-go/jwx/v3/jwt"
	"github.com/stretchr/testify/require"
	"xorm.io/xorm"

	"github.com/talgent-work/workbench/internal/business/account/application"
	"github.com/talgent-work/workbench/internal/business/account/domain"
	"github.com/talgent-work/workbench/internal/business/account/infrastructure"
	accounthttp "github.com/talgent-work/workbench/internal/business/account/transport/http"
	"github.com/talgent-work/workbench/pkg/mysqltest"
)

const (
	issuer   = "https://workspace.test/auth"
	audience = "talgent"
)

var lifetimes = domain.SessionLifetimes{Access: 15 * time.Minute, Sliding: 30 * 24 * time.Hour, Absolute: 90 * 24 * time.Hour}

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time          { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *clock) Advance(d time.Duration) { c.mu.Lock(); c.now = c.now.Add(d); c.mu.Unlock() }

type githubUser struct {
	ID     int64
	Login  string
	Name   string
	Emails []map[string]any
}

// fakeGitHub answers the three GitHub endpoints sign-in uses. A code names
// the user it signs in; "stale" is a code GitHub no longer accepts.
type fakeGitHub struct {
	mu        sync.Mutex
	users     map[string]githubUser
	verifiers map[string]string
}

func (g *fakeGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/login/oauth/access_token":
		_ = r.ParseForm()
		code := r.PostForm.Get("code")
		if _, ok := g.users[code]; !ok || r.PostForm.Get("client_secret") != "secret" {
			fmt.Fprint(w, `{"error":"bad_verification_code"}`)
			return
		}
		g.verifiers[code] = r.PostForm.Get("code_verifier")
		fmt.Fprintf(w, `{"access_token":"token-%s","token_type":"bearer"}`, code)
	case "/user", "/user/emails":
		user, ok := g.users[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer token-")]
		if !ok {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/user" {
			_ = json.NewEncoder(w).Encode(map[string]any{"id": user.ID, "login": user.Login, "name": user.Name, "avatar_url": fmt.Sprintf("https://avatars.githubusercontent.com/u/%d", user.ID)})
			return
		}
		_ = json.NewEncoder(w).Encode(user.Emails)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (g *fakeGitHub) set(code string, user githubUser) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.users[code] = user
}

func verifiedEmail(address string) []map[string]any {
	return []map[string]any{{"email": "old@example.com", "primary": false, "verified": true}, {"email": address, "primary": true, "verified": true}}
}

type harness struct {
	t      *testing.T
	clock  *clock
	engine *xorm.Engine
	github *fakeGitHub
	app    *application.Application
	keys   *infrastructure.SigningKeys
	server *httptest.Server
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dsn := mysqltest.Database(t)
	engine, err := xorm.NewEngine("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = engine.Close() })
	c := &clock{now: time.Now().UTC().Truncate(time.Millisecond)}
	gh := &fakeGitHub{users: map[string]githubUser{}, verifiers: map[string]string{}}
	ghServer := httptest.NewServer(gh)
	t.Cleanup(ghServer.Close)

	keys, err := infrastructure.NewSigningKeys(engine, infrastructure.SigningKeyConfig{
		Issuer: issuer, Audience: audience, MasterKey: make([]byte, 32),
		Overlap: 10 * time.Minute, RotationInterval: 30 * 24 * time.Hour,
		AccessLifetime: lifetimes.Access, VerificationRetention: lifetimes.Absolute, Now: c.Now,
	})
	require.NoError(t, err)
	github, err := infrastructure.NewGitHubIdentity(infrastructure.GitHubOAuthConfig{
		ClientID: "client", ClientSecret: "secret", RedirectURL: "https://workspace.test/auth/github/callback",
		WebBaseURL: ghServer.URL, APIBaseURL: ghServer.URL,
	}, ghServer.Client())
	require.NoError(t, err)
	sessions := infrastructure.NewLoginSessionRepository(engine)
	app := application.NewApplication(application.Dependencies{
		Accounts: infrastructure.NewAccountRepository(engine), Sessions: sessions, GitHub: github,
		Tokens: keys, Retention: sessions, Directory: infrastructure.NewUserDirectoryQueries(engine),
		Lifetimes: lifetimes, Now: c.Now,
	})
	require.NoError(t, app.RotateSigningKeys(context.Background()))
	handler, err := accounthttp.NewHandler(app, accounthttp.Config{
		LoginAttemptKey: []byte(strings.Repeat("k", 32)), LoginAttemptTTL: 10 * time.Minute, Now: c.Now,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)
	mux := http.NewServeMux()
	mux.Handle(accounthttp.PathPrefix, handler)
	server := httptest.NewTLSServer(mux)
	t.Cleanup(server.Close)
	return &harness{t: t, clock: c, engine: engine, github: gh, app: app, keys: keys, server: server}
}

// device is one browser: its own cookie jar, never following redirects.
type device struct {
	h      *harness
	client *http.Client
}

func (h *harness) device() *device {
	jar, err := cookiejar.New(nil)
	require.NoError(h.t, err)
	client := &http.Client{
		Transport:     h.server.Client().Transport,
		Jar:           jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return &device{h: h, client: client}
}

func (d *device) get(path string) *http.Response {
	d.h.t.Helper()
	resp, err := d.client.Get(d.h.server.URL + path)
	require.NoError(d.h.t, err)
	_ = resp.Body.Close()
	return resp
}

func (d *device) post(path, contentType string) *http.Response {
	d.h.t.Helper()
	req, err := http.NewRequest(http.MethodPost, d.h.server.URL+path, strings.NewReader("{}"))
	require.NoError(d.h.t, err)
	req.Header.Set("Content-Type", contentType)
	resp, err := d.client.Do(req)
	require.NoError(d.h.t, err)
	_ = resp.Body.Close()
	return resp
}

func (d *device) cookie(name, path string) string {
	u, _ := url.Parse(d.h.server.URL + path)
	for _, cookie := range d.client.Jar.Cookies(u) {
		if cookie.Name == name {
			return cookie.Value
		}
	}
	return ""
}

// start begins a sign-in and returns the state GitHub would echo.
func (d *device) start(next string) string {
	d.h.t.Helper()
	resp := d.get("/auth/github/login?next=" + url.QueryEscape(next))
	require.Equal(d.h.t, http.StatusFound, resp.StatusCode)
	authorize, err := url.Parse(resp.Header.Get("Location"))
	require.NoError(d.h.t, err)
	require.Equal(d.h.t, "/login/oauth/authorize", authorize.Path)
	require.Equal(d.h.t, "read:user user:email", authorize.Query().Get("scope"))
	require.Equal(d.h.t, "S256", authorize.Query().Get("code_challenge_method"))
	return authorize.Query().Get("state")
}

func (d *device) callback(query string) *http.Response {
	return d.get("/auth/github/callback?" + query)
}

func (d *device) signIn(code string) *http.Response {
	state := d.start("/projects/alpha?tab=works")
	return d.callback(url.Values{"code": {code}, "state": {state}}.Encode())
}

func (h *harness) accounts() []map[string]string {
	rows, err := h.engine.QueryString("SELECT id, github_user_id, username, email, display_name FROM account ORDER BY created_at")
	require.NoError(h.t, err)
	return rows
}

func (h *harness) verify(token string) jwt.Token {
	h.t.Helper()
	body, err := h.app.PublishedKeys(context.Background())
	require.NoError(h.t, err)
	set, err := jwk.Parse(body)
	require.NoError(h.t, err)
	parsed, err := jwt.Parse([]byte(token), jwt.WithKeySet(set), jwt.WithValidate(true),
		jwt.WithIssuer(issuer), jwt.WithAudience(audience), jwt.WithClock(jwt.ClockFunc(h.clock.Now)))
	require.NoError(h.t, err)
	return parsed
}

func mustURL(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	return u
}
