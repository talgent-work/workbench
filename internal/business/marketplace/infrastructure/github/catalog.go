package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
	"golang.org/x/sync/errgroup"
)

// installationFetchConcurrency bounds parallel repository listings across one
// user's installations.
const installationFetchConcurrency = 4

// Catalog observes GitHub's current access decisions with the owner's user
// token. One authorization reaches every installation of the App the user can
// access. The App's own declaration is read once per process.
type Catalog struct {
	enabled        bool
	app            *appClient
	authorizations application.AuthorizationStore
	cipher         TokenCipher
	now            func() time.Time

	// The App's slug and the permissions connecting requests change only when
	// the App is edited; each is read once per process.
	declarationMu sync.Mutex
	slug          string
	requested     []application.ProviderPermission
}

func NewCatalog(cfg Config, authorizations application.AuthorizationStore, cipher TokenCipher) (*Catalog, error) {
	app, err := newAppClient(cfg)
	if err != nil {
		return nil, err
	}
	enabled, err := cfg.enabled()
	if err != nil {
		return nil, err
	}
	if enabled {
		if authorizations == nil {
			return nil, fmt.Errorf("github catalog requires provider authorization repository")
		}
		if cipher == nil {
			return nil, fmt.Errorf("github catalog requires token cipher")
		}
	}
	return &Catalog{enabled: enabled, app: app, authorizations: authorizations, cipher: cipher, now: time.Now}, nil
}

// ObserveAccess lists the user's installations with their repositories, the
// installations fetched concurrently. An installation that disappears between
// the two reads only narrows the scope.
func (c *Catalog) ObserveAccess(ctx context.Context, ownerID string, provider domain.Provider) (domain.AccessScope, error) {
	if !c.enabled || provider != domain.ProviderGitHub {
		return domain.AccessScope{}, application.ErrProviderUnavailable
	}
	// The observation is as of its start: a slower one must not look newer
	// than one that began after it.
	observedAt := c.now().UTC()
	session, err := c.userSession(ctx, ownerID)
	if err != nil {
		return domain.AccessScope{}, mapProviderError(err)
	}
	var installations []githubInstallation
	for page := 1; ; page++ {
		var response struct {
			Installations []githubInstallation `json:"installations"`
		}
		if err := session.get(ctx, fmt.Sprintf("/user/installations?per_page=100&page=%d", page), &response); err != nil {
			return domain.AccessScope{}, mapProviderError(err)
		}
		installations = append(installations, response.Installations...)
		if len(response.Installations) < 100 {
			break
		}
	}
	accounts := make([]*domain.ScopeAccount, len(installations))
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(installationFetchConcurrency)
	for index, installation := range installations {
		group.Go(func() error {
			repositories, err := session.installationRepositories(groupCtx, installation.ID)
			// Only an installation GitHub no longer has narrows the scope; a
			// refusal such as a rate limit fails the whole observation.
			if isGitHubGone(err) {
				return nil
			}
			if err != nil {
				return mapProviderError(err)
			}
			accounts[index] = &domain.ScopeAccount{
				InstallationID: strconv.FormatInt(installation.ID, 10),
				Login:          installation.Account.Login, Type: installation.Account.Type, AvatarURL: installation.Account.AvatarURL,
				RepositorySelection: installation.RepositorySelection, SettingsURL: c.installationSettingsURL(installation),
				Repositories: repositories,
			}
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return domain.AccessScope{}, err
	}
	scope := domain.AccessScope{Permissions: mergePermissions(installations), ObservedAt: observedAt}
	for _, account := range accounts {
		if account != nil {
			scope.Accounts = append(scope.Accounts, *account)
		}
	}
	slices.SortFunc(scope.Accounts, func(a, b domain.ScopeAccount) int {
		return strings.Compare(strings.ToLower(a.Login), strings.ToLower(b.Login))
	})
	return scope, nil
}

// RequestedPermissions reads the App's public permission declaration, which is
// what connecting requests. It needs no user credential.
func (c *Catalog) RequestedPermissions(ctx context.Context, provider domain.Provider) ([]application.ProviderPermission, error) {
	if !c.enabled || provider != domain.ProviderGitHub {
		return nil, application.ErrProviderUnavailable
	}
	requested, err := c.requestedPermissions(ctx)
	if err != nil {
		return nil, err
	}
	return slices.Clone(requested), nil
}

// ManageURL opens GitHub's installation flow, where the user adds or changes
// the accounts and repositories the App reaches.
func (c *Catalog) ManageURL(ctx context.Context, provider domain.Provider) (string, error) {
	if !c.enabled || provider != domain.ProviderGitHub {
		return "", application.ErrProviderUnavailable
	}
	slug, err := c.appSlug(ctx)
	if err != nil {
		return "", err
	}
	return c.app.cfg.webBaseURL() + "/apps/" + url.PathEscape(slug) + "/installations/new", nil
}

// appSlug is the configured slug, or the one GitHub reports, read once.
func (c *Catalog) appSlug(ctx context.Context) (string, error) {
	c.declarationMu.Lock()
	defer c.declarationMu.Unlock()
	return c.appSlugLocked(ctx)
}

func (c *Catalog) appSlugLocked(ctx context.Context) (string, error) {
	if c.slug != "" {
		return c.slug, nil
	}
	slug, err := c.app.slug(ctx)
	if err != nil {
		return "", mapProviderError(err)
	}
	c.slug = slug
	return slug, nil
}

// requestedPermissions reads the App's public declaration once; a failed read
// is retried by the next caller.
func (c *Catalog) requestedPermissions(ctx context.Context) ([]application.ProviderPermission, error) {
	c.declarationMu.Lock()
	defer c.declarationMu.Unlock()
	if c.requested != nil {
		return c.requested, nil
	}
	slug, err := c.appSlugLocked(ctx)
	if err != nil {
		return nil, err
	}
	var response struct {
		Permissions map[string]string `json:"permissions"`
	}
	if err := c.app.doJSON(ctx, http.MethodGet, "/apps/"+url.PathEscape(slug), "", nil, &response); err != nil {
		return nil, mapProviderError(err)
	}
	c.requested = sortedPermissions(response.Permissions)
	return c.requested, nil
}

func (c *Catalog) installationSettingsURL(installation githubInstallation) string {
	id := strconv.FormatInt(installation.ID, 10)
	if installation.Account.Type == "Organization" {
		return c.app.cfg.webBaseURL() + "/organizations/" + url.PathEscape(installation.Account.Login) + "/settings/installations/" + id
	}
	return c.app.cfg.webBaseURL() + "/settings/installations/" + id
}

// userSession holds a fresh user token and retries a request once after a
// forced refresh when GitHub rejects the token. Concurrent requests share one
// refresh.
type userSession struct {
	catalog *Catalog
	mu      sync.Mutex
	auth    *domain.ProviderAuthorization
	token   string
}

func (c *Catalog) userSession(ctx context.Context, ownerID string) (*userSession, error) {
	auth, err := c.authorizations.GetByOwnerAndProvider(ctx, ownerID, domain.ProviderGitHub)
	if err != nil {
		return nil, err
	}
	manager := newFreshUserTokenManager(c.app.cfg, c.app.http, c.authorizations, c.cipher, c.now)
	auth, token, err := manager.EnsureFreshUserToken(ctx, EnsureFreshUserTokenInput{AuthorizationID: auth.ID, OwnerID: ownerID, Provider: domain.ProviderGitHub, Reason: "catalog"})
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("github user access token is empty")
	}
	return &userSession{catalog: c, auth: auth, token: token}, nil
}

func (s *userSession) get(ctx context.Context, path string, out any) error {
	s.mu.Lock()
	rejected := s.token
	s.mu.Unlock()
	err := s.catalog.app.doJSON(ctx, http.MethodGet, path, "Bearer "+rejected, nil, out)
	if !isGitHubUnauthorized(err) {
		return mapRequestError(err)
	}
	token, err := s.refresh(ctx, rejected)
	if err != nil {
		return mapProviderError(err)
	}
	err = s.catalog.app.doJSON(ctx, http.MethodGet, path, "Bearer "+token, nil, out)
	if isGitHubUnauthorized(err) {
		return application.NewProviderError(providerErrorGitHubTokenRefreshFailed, false, "github api unauthorized after refresh", err)
	}
	return mapRequestError(err)
}

// refresh replaces a rejected token once; a request that saw the same rejected
// token reuses the replacement.
func (s *userSession) refresh(ctx context.Context, rejected string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token != rejected {
		return s.token, nil
	}
	manager := newFreshUserTokenManager(s.catalog.app.cfg, s.catalog.app.http, s.catalog.authorizations, s.catalog.cipher, s.catalog.now)
	auth, token, err := manager.EnsureFreshUserToken(ctx, EnsureFreshUserTokenInput{AuthorizationID: s.auth.ID, OwnerID: s.auth.OwnerID, Provider: domain.ProviderGitHub, ForceRefresh: true, Reason: "github_api_401"})
	if err != nil {
		return "", err
	}
	s.auth, s.token = auth, token
	return token, nil
}

func (s *userSession) installationRepositories(ctx context.Context, installationID int64) ([]domain.ScopeRepository, error) {
	var repositories []domain.ScopeRepository
	for page := 1; ; page++ {
		var response struct {
			Repositories []githubRepository `json:"repositories"`
		}
		if err := s.get(ctx, fmt.Sprintf("/user/installations/%d/repositories?per_page=100&page=%d", installationID, page), &response); err != nil {
			return nil, err
		}
		for _, repository := range response.Repositories {
			repositories = append(repositories, domain.ScopeRepository{
				RepoID:   "github:" + strconv.FormatInt(repository.ID, 10),
				RepoName: strings.TrimSpace(repository.FullName), CloneURL: strings.TrimSpace(repository.CloneURL),
				WebURL: strings.TrimSpace(repository.HTMLURL), DefaultBranch: strings.TrimSpace(repository.DefaultBranch),
			})
		}
		if len(response.Repositories) < 100 {
			slices.SortFunc(repositories, func(a, b domain.ScopeRepository) int { return strings.Compare(a.RepoName, b.RepoName) })
			return repositories, nil
		}
	}
}

type githubInstallation struct {
	ID      int64 `json:"id"`
	Account struct {
		Login     string `json:"login"`
		Type      string `json:"type"`
		AvatarURL string `json:"avatar_url"`
	} `json:"account"`
	RepositorySelection string            `json:"repository_selection"`
	Permissions         map[string]string `json:"permissions"`
}

type githubRepository struct {
	ID            int64  `json:"id"`
	FullName      string `json:"full_name"`
	CloneURL      string `json:"clone_url"`
	HTMLURL       string `json:"html_url"`
	DefaultBranch string `json:"default_branch"`
}

// mergePermissions keeps the strongest access granted by any installation.
func mergePermissions(installations []githubInstallation) []domain.ScopePermission {
	rank := map[string]int{"read": 1, "write": 2, "admin": 3}
	merged := map[string]string{}
	for _, installation := range installations {
		for name, access := range installation.Permissions {
			if rank[access] > rank[merged[name]] {
				merged[name] = access
			}
		}
	}
	result := make([]domain.ScopePermission, 0, len(merged))
	for name, access := range merged {
		result = append(result, domain.ScopePermission{Name: name, Access: access})
	}
	slices.SortFunc(result, func(a, b domain.ScopePermission) int { return strings.Compare(a.Name, b.Name) })
	return result
}

func sortedPermissions(permissions map[string]string) []application.ProviderPermission {
	result := make([]application.ProviderPermission, 0, len(permissions))
	for name, access := range permissions {
		result = append(result, application.ProviderPermission{Name: name, Access: access})
	}
	slices.SortFunc(result, func(a, b application.ProviderPermission) int { return strings.Compare(a.Name, b.Name) })
	return result
}

func mapProviderError(err error) error {
	if errors.Is(err, domain.ErrRepositoryNotFound) {
		return err
	}
	var providerErr *application.ProviderError
	if errors.As(err, &providerErr) {
		return err
	}
	return fmt.Errorf("%w: %v", application.ErrProviderUnavailable, err)
}

func mapRequestError(err error) error {
	if err == nil || isGitHubNotFound(err) {
		return err
	}
	return mapProviderError(err)
}

func isGitHubUnauthorized(err error) bool {
	var statusErr githubAPIStatusError
	return errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusUnauthorized
}

func isGitHubGone(err error) bool {
	var statusErr githubAPIStatusError
	return errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusNotFound
}

func isGitHubNotFound(err error) bool {
	var statusErr githubAPIStatusError
	return errors.As(err, &statusErr) && (statusErr.StatusCode == http.StatusNotFound || statusErr.StatusCode == http.StatusForbidden)
}
