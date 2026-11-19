package github

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/talgent-work/workbench/internal/business/marketplace/infrastructure/oauthtoken"
)

const (
	defaultAPIBaseURL = "https://api.github.com"
	defaultAPIVersion = "2026-03-10"
)

// Config is the GitHub App adapter beside the github Provider declaration.
// The App settings are its own; the OAuth client, its endpoints and the
// credential cipher key come from the declaration and the connection
// settings, and the adapter is enabled exactly when github is declared.
type Config struct {
	AppID         string `json:"app-id" toml:"app-id" yaml:"app-id"`
	AppSlug       string `json:"app-slug" toml:"app-slug" yaml:"app-slug"`
	PrivateKeyPEM string `json:"private-key-pem" toml:"private-key-pem" yaml:"private-key-pem"`
	WebBaseURL    string `json:"web-base-url" toml:"web-base-url" yaml:"web-base-url"`
	APIBaseURL    string `json:"api-base-url" toml:"api-base-url" yaml:"api-base-url"`
	APIVersion    string `json:"api-version" toml:"api-version" yaml:"api-version"`
	WebhookSecret string `json:"webhook-secret" toml:"webhook-secret" yaml:"webhook-secret"`

	Enabled               bool   `json:"-" toml:"-" yaml:"-"`
	OAuthClientID         string `json:"-" toml:"-" yaml:"-"`
	OAuthClientSecret     string `json:"-" toml:"-" yaml:"-"`
	AuthorizationEndpoint string `json:"-" toml:"-" yaml:"-"`
	TokenEndpoint         string `json:"-" toml:"-" yaml:"-"`
	TokenCipherKey        string `json:"-" toml:"-" yaml:"-"`
}

func (c Config) enabled() (bool, error) {
	return c.Enabled, nil
}

func (c Config) authorizationEndpoint() string {
	if endpoint := strings.TrimSpace(c.AuthorizationEndpoint); endpoint != "" {
		return endpoint
	}
	return c.webBaseURL() + "/login/oauth/authorize"
}

// tokenClient is the OAuth App at GitHub's token endpoint, which takes the
// client's credentials in the form.
func (c Config) tokenClient() oauthtoken.Endpoint {
	return oauthtoken.Endpoint{URL: c.tokenEndpoint(), ClientID: strings.TrimSpace(c.OAuthClientID), ClientSecret: strings.TrimSpace(c.OAuthClientSecret),
		Authentication: oauthtoken.ClientSecretPost}
}

func (c Config) tokenEndpoint() string {
	if endpoint := strings.TrimSpace(c.TokenEndpoint); endpoint != "" {
		return endpoint
	}
	return c.webBaseURL() + "/login/oauth/access_token"
}

func (c Config) validate() error {
	enabled, err := c.enabled()
	if err != nil {
		return err
	}
	if !enabled {
		return nil
	}
	missing := make([]string, 0, 5)
	if strings.TrimSpace(c.AppID) == "" {
		missing = append(missing, "app-id")
	}
	if strings.TrimSpace(c.PrivateKeyPEM) == "" {
		missing = append(missing, "private-key-pem")
	}
	if strings.TrimSpace(c.OAuthClientID) == "" {
		missing = append(missing, "client-id")
	}
	if strings.TrimSpace(c.OAuthClientSecret) == "" {
		missing = append(missing, "client-secret")
	}
	if strings.TrimSpace(c.TokenCipherKey) == "" {
		missing = append(missing, "connections.credential-cipher-key")
	}
	if len(missing) > 0 {
		return fmt.Errorf("the github Provider is declared but its App lacks: %s", strings.Join(missing, ", "))
	}
	return nil
}

func (c Config) apiBaseURL() string {
	if baseURL := strings.TrimSpace(c.APIBaseURL); baseURL != "" {
		return strings.TrimRight(baseURL, "/")
	}
	return defaultAPIBaseURL
}

func (c Config) webBaseURL() string {
	if baseURL := strings.TrimSpace(c.WebBaseURL); baseURL != "" {
		return strings.TrimRight(baseURL, "/")
	}
	return "https://github.com"
}

func (c Config) apiVersion() string {
	if version := strings.TrimSpace(c.APIVersion); version != "" {
		return version
	}
	return defaultAPIVersion
}

type appClient struct {
	cfg        Config
	http       *http.Client
	privateKey *rsa.PrivateKey
}

func newAppClient(cfg Config) (*appClient, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	enabled, err := cfg.enabled()
	if err != nil {
		return nil, err
	}
	if !enabled {
		return &appClient{cfg: cfg, http: http.DefaultClient}, nil
	}
	key, err := parsePrivateKey(cfg.PrivateKeyPEM)
	if err != nil {
		return nil, err
	}
	return &appClient{cfg: cfg, http: http.DefaultClient, privateKey: key}, nil
}

func (c *appClient) doJSON(ctx context.Context, method, path, authorization string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.cfg.apiBaseURL()+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", c.cfg.apiVersion())
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return githubAPIStatusError{StatusCode: resp.StatusCode}
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

type githubAPIStatusError struct {
	StatusCode int
}

func (e githubAPIStatusError) Error() string {
	return fmt.Sprintf("github api status %d", e.StatusCode)
}

func (c *appClient) signJWT(now func() time.Time) (string, error) {
	if c.privateKey == nil {
		return "", fmt.Errorf("github app private key is not configured")
	}
	issuedAt := now().UTC().Add(-1 * time.Minute).Unix()
	claims := map[string]any{
		"iat": issuedAt,
		"exp": issuedAt + int64((9 * time.Minute).Seconds()),
		"iss": strings.TrimSpace(c.cfg.AppID),
	}
	header := map[string]string{"alg": "RS256", "typ": "JWT"}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	signingInput := base64.RawURLEncoding.EncodeToString(headerJSON) + "." + base64.RawURLEncoding.EncodeToString(claimsJSON)
	sum := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, c.privateKey, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func parsePrivateKey(raw string) (*rsa.PrivateKey, error) {
	normalized := strings.ReplaceAll(strings.TrimSpace(raw), `\n`, "\n")
	block, _ := pem.Decode([]byte(normalized))
	if block == nil {
		return nil, fmt.Errorf("github app private key pem is invalid")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("github app private key parse: %w", err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("github app private key must be RSA")
	}
	return key, nil
}

// slug returns the configured App slug, reading it with the App JWT when the
// deployment leaves it unset.
func (c *appClient) slug(ctx context.Context) (string, error) {
	if slug := strings.TrimSpace(c.cfg.AppSlug); slug != "" {
		return slug, nil
	}
	jwt, err := c.signJWT(time.Now)
	if err != nil {
		return "", err
	}
	var response struct {
		Slug string `json:"slug"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/app", "Bearer "+jwt, nil, &response); err != nil {
		return "", err
	}
	if strings.TrimSpace(response.Slug) == "" {
		return "", fmt.Errorf("github app response missing slug")
	}
	return strings.TrimSpace(response.Slug), nil
}
