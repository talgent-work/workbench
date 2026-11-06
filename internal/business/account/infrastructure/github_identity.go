package infrastructure

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/samber/oops"

	"github.com/talgent-work/workbench/internal/business/account/application"
)

// GitHubOAuthConfig names the login OAuth App. It is not the Marketplace
// Provider App: sign-in asks only for identity scopes.
type GitHubOAuthConfig struct {
	ClientID     string
	ClientSecret string
	// RedirectURL is the public /auth/github/callback address registered on the App.
	RedirectURL string
	WebBaseURL  string
	APIBaseURL  string
}

func (c GitHubOAuthConfig) validate() error {
	for name, value := range map[string]string{"client id": c.ClientID, "client secret": c.ClientSecret, "redirect url": c.RedirectURL, "web base url": c.WebBaseURL, "api base url": c.APIBaseURL} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("account github oauth: %s is required", name)
		}
	}
	return nil
}

const githubLoginScopes = "read:user user:email"

// GitHubIdentity runs the authorization-code flow against GitHub. The access
// token it receives lives only inside Identify.
type GitHubIdentity struct {
	cfg  GitHubOAuthConfig
	http *http.Client
}

var _ application.GitHubIdentityPort = (*GitHubIdentity)(nil)

func NewGitHubIdentity(cfg GitHubOAuthConfig, client *http.Client) (*GitHubIdentity, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	cfg.WebBaseURL = strings.TrimRight(cfg.WebBaseURL, "/")
	cfg.APIBaseURL = strings.TrimRight(cfg.APIBaseURL, "/")
	return &GitHubIdentity{cfg: cfg, http: client}, nil
}

func (g *GitHubIdentity) AuthorizationURL(state, codeChallenge string) string {
	query := url.Values{
		"client_id":             {g.cfg.ClientID},
		"redirect_uri":          {g.cfg.RedirectURL},
		"scope":                 {githubLoginScopes},
		"state":                 {state},
		"code_challenge":        {codeChallenge},
		"code_challenge_method": {"S256"},
	}
	return g.cfg.WebBaseURL + "/login/oauth/authorize?" + query.Encode()
}

func (g *GitHubIdentity) Identify(ctx context.Context, code, codeVerifier string) (application.GitHubIdentity, error) {
	token, err := g.exchange(ctx, code, codeVerifier)
	if err != nil {
		return application.GitHubIdentity{}, err
	}
	var user struct {
		ID        int64  `json:"id"`
		Login     string `json:"login"`
		Name      string `json:"name"`
		AvatarURL string `json:"avatar_url"`
	}
	if err := g.getJSON(ctx, token, "/user", &user); err != nil {
		return application.GitHubIdentity{}, err
	}
	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err := g.getJSON(ctx, token, "/user/emails", &emails); err != nil {
		return application.GitHubIdentity{}, err
	}
	identity := application.GitHubIdentity{
		UserID:      user.ID,
		Login:       strings.TrimSpace(user.Login),
		DisplayName: strings.TrimSpace(user.Name),
		AvatarURL:   strings.TrimSpace(user.AvatarURL),
	}
	// GitHub reports no name for many accounts; the login is what the
	// person is known by there, as the retired identity service showed.
	if identity.DisplayName == "" {
		identity.DisplayName = identity.Login
	}
	for _, email := range emails {
		if email.Primary && email.Verified {
			identity.Email = strings.TrimSpace(email.Email)
		}
	}
	return identity, nil
}

func (g *GitHubIdentity) exchange(ctx context.Context, code, codeVerifier string) (string, error) {
	form := url.Values{
		"client_id":     {g.cfg.ClientID},
		"client_secret": {g.cfg.ClientSecret},
		"code":          {code},
		"redirect_uri":  {g.cfg.RedirectURL},
		"code_verifier": {codeVerifier},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.cfg.WebBaseURL+"/login/oauth/access_token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	var out struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := g.do(req, &out); err != nil {
		return "", err
	}
	switch {
	case out.Error == "bad_verification_code":
		return "", application.ErrSignInExpired
	case out.Error != "":
		return "", oops.With("operation", "github.exchange").With("github_error", out.Error).Errorf("github rejected the code exchange")
	case out.AccessToken == "":
		return "", oops.With("operation", "github.exchange").Errorf("github returned no access token")
	}
	return out.AccessToken, nil
}

func (g *GitHubIdentity) getJSON(ctx context.Context, token, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.cfg.APIBaseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	return g.do(req, out)
}

func (g *GitHubIdentity) do(req *http.Request, out any) error {
	resp, err := g.http.Do(req)
	if err != nil {
		return oops.With("operation", "github.request").With("path", req.URL.Path).Wrap(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return oops.With("operation", "github.request").With("path", req.URL.Path).With("status", resp.StatusCode).Errorf("github answered %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out); err != nil {
		return oops.With("operation", "github.decode").With("path", req.URL.Path).Wrap(errors.Join(errors.New("malformed github response"), err))
	}
	return nil
}
