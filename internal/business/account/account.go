// Package account composes the Account context: GitHub sign-in, Login
// Sessions, Identity Token keys and the User Directory.
package account

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"connectrpc.com/connect"
	"go.uber.org/fx"
	"xorm.io/xorm"

	"github.com/talgent-work/workbench/gen/go/talgent/account/private/v1/accountprivatev1connect"
	"github.com/talgent-work/workbench/gen/go/talgent/account/public/v1/accountpublicv1connect"
	"github.com/talgent-work/workbench/internal/business/account/application"
	"github.com/talgent-work/workbench/internal/business/account/domain"
	"github.com/talgent-work/workbench/internal/business/account/infrastructure"
	accountconnect "github.com/talgent-work/workbench/internal/business/account/transport/connectrpc"
	accounthttp "github.com/talgent-work/workbench/internal/business/account/transport/http"
	"github.com/talgent-work/workbench/pkg/authn"
	"github.com/talgent-work/workbench/pkg/connectlog"
	pkgmysql "github.com/talgent-work/workbench/pkg/mysql"
)

// Database is the name of Account's own MySQL connection (talgent_account).
const Database = "account"

// Config is Account's platform policy and the login OAuth App.
type Config struct {
	// Issuer is the iss claim, the public /auth base URL.
	Issuer string `json:"issuer" yaml:"issuer" toml:"issuer"`
	// Audience is the aud claim the gateway expects.
	Audience string `json:"audience" yaml:"audience" toml:"audience"`
	// CookieDomain is empty for host-only cookies; the parent domain of every
	// entry lets one sign-in serve them all.
	CookieDomain string `json:"cookie-domain" yaml:"cookie-domain" toml:"cookie-domain"`
	// ReturnOrigins are the base addresses of the other entries a sign-in may
	// return to, such as https://app.example.com beside the Workbench
	// that receives GitHub's callback.
	ReturnOrigins           []string `json:"return-origins" yaml:"return-origins" toml:"return-origins"`
	AccessTokenLifetime     string   `json:"access-token-lifetime" yaml:"access-token-lifetime" toml:"access-token-lifetime"`
	SessionSlidingLifetime  string   `json:"session-sliding-lifetime" yaml:"session-sliding-lifetime" toml:"session-sliding-lifetime"`
	SessionAbsoluteLifetime string   `json:"session-absolute-lifetime" yaml:"session-absolute-lifetime" toml:"session-absolute-lifetime"`
	KeyRotationInterval     string   `json:"key-rotation-interval" yaml:"key-rotation-interval" toml:"key-rotation-interval"`
	// KeyOverlap is how long a new key is published before it signs.
	KeyOverlap string `json:"key-overlap" yaml:"key-overlap" toml:"key-overlap"`
	// MasterKeyPath is a file holding 32 base64-encoded bytes that seal
	// signing keys at rest and sign in-progress sign-ins.
	MasterKeyPath string       `json:"master-key-path" yaml:"master-key-path" toml:"master-key-path"`
	GitHub        GitHubConfig `json:"github" yaml:"github" toml:"github"`
}

// GitHubConfig is the login OAuth App, distinct from the Marketplace Provider App.
type GitHubConfig struct {
	ClientID     string `json:"client-id" yaml:"client-id" toml:"client-id"`
	ClientSecret string `json:"client-secret" yaml:"client-secret" toml:"client-secret"`
	RedirectURL  string `json:"redirect-url" yaml:"redirect-url" toml:"redirect-url"`
	WebBaseURL   string `json:"web-base-url" yaml:"web-base-url" toml:"web-base-url"`
	APIBaseURL   string `json:"api-base-url" yaml:"api-base-url" toml:"api-base-url"`
}

type policy struct {
	lifetimes        domain.SessionLifetimes
	rotationInterval time.Duration
	overlap          time.Duration
}

func (c Config) policy() (policy, error) {
	var p policy
	for _, field := range []struct {
		name   string
		raw    string
		target *time.Duration
	}{
		{"access-token-lifetime", c.AccessTokenLifetime, &p.lifetimes.Access},
		{"session-sliding-lifetime", c.SessionSlidingLifetime, &p.lifetimes.Sliding},
		{"session-absolute-lifetime", c.SessionAbsoluteLifetime, &p.lifetimes.Absolute},
		{"key-rotation-interval", c.KeyRotationInterval, &p.rotationInterval},
		{"key-overlap", c.KeyOverlap, &p.overlap},
	} {
		parsed, err := time.ParseDuration(strings.TrimSpace(field.raw))
		if err != nil || parsed <= 0 {
			return policy{}, fmt.Errorf("account config: %s must be a positive duration", field.name)
		}
		*field.target = parsed
	}
	if p.lifetimes.Access > p.lifetimes.Sliding || p.lifetimes.Sliding > p.lifetimes.Absolute {
		return policy{}, errors.New("account config: lifetimes must satisfy access <= sliding <= absolute")
	}
	return p, nil
}

// Validate checks the configuration without reading the master key file.
func (c Config) Validate() error {
	if _, err := c.policy(); err != nil {
		return err
	}
	if strings.TrimSpace(c.MasterKeyPath) == "" {
		return errors.New("account config: master-key-path is required")
	}
	return nil
}

func readMasterKey(path string) ([]byte, error) {
	raw, err := os.ReadFile(strings.TrimSpace(path))
	if err != nil {
		return nil, fmt.Errorf("account master key: %w", err)
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || len(key) != 32 {
		return nil, errors.New("account master key must be 32 base64-encoded bytes")
	}
	return key, nil
}

// deriveKey separates the master key's uses.
func deriveKey(master []byte, purpose string) []byte {
	mac := hmac.New(sha256.New, master)
	mac.Write([]byte("talgent-account/" + purpose))
	return mac.Sum(nil)
}

type runtime struct {
	fx.Out
	Policy     policy
	SigningKey infrastructure.SigningKeyConfig
	HTTP       accounthttp.Config
	GitHub     infrastructure.GitHubOAuthConfig
}

func newRuntime(cfg Config) (runtime, error) {
	p, err := cfg.policy()
	if err != nil {
		return runtime{}, err
	}
	master, err := readMasterKey(cfg.MasterKeyPath)
	if err != nil {
		return runtime{}, err
	}
	return runtime{
		Policy: p,
		SigningKey: infrastructure.SigningKeyConfig{
			Issuer:                cfg.Issuer,
			Audience:              cfg.Audience,
			MasterKey:             deriveKey(master, "signing-key-seal"),
			Overlap:               p.overlap,
			RotationInterval:      p.rotationInterval,
			AccessLifetime:        p.lifetimes.Access,
			VerificationRetention: p.lifetimes.Absolute,
		},
		HTTP: accounthttp.Config{
			CookieDomain:    strings.TrimSpace(cfg.CookieDomain),
			LoginAttemptKey: deriveKey(master, "login-attempt"),
			LoginAttemptTTL: 10 * time.Minute,
			CallbackURL:     cfg.GitHub.RedirectURL,
			ReturnOrigins:   cfg.ReturnOrigins,
		},
		GitHub: infrastructure.GitHubOAuthConfig{
			ClientID:     cfg.GitHub.ClientID,
			ClientSecret: cfg.GitHub.ClientSecret,
			RedirectURL:  cfg.GitHub.RedirectURL,
			WebBaseURL:   cfg.GitHub.WebBaseURL,
			APIBaseURL:   cfg.GitHub.APIBaseURL,
		},
	}, nil
}

func newApplication(engine *xorm.Engine, p policy, keys *infrastructure.SigningKeys, github *infrastructure.GitHubIdentity) *application.Application {
	sessions := infrastructure.NewLoginSessionRepository(engine)
	return application.NewApplication(application.Dependencies{
		Accounts:  infrastructure.NewAccountRepository(engine),
		Sessions:  sessions,
		GitHub:    github,
		Tokens:    keys,
		Retention: sessions,
		Directory: infrastructure.NewUserDirectoryQueries(engine),
		Lifetimes: p.lifetimes,
	})
}

func newGitHubIdentity(cfg infrastructure.GitHubOAuthConfig) (*infrastructure.GitHubIdentity, error) {
	return infrastructure.NewGitHubIdentity(cfg, &http.Client{Timeout: 10 * time.Second})
}

type registration struct {
	fx.In
	App     *application.Application
	Mux     *http.ServeMux
	Logger  *slog.Logger
	Handler *accounthttp.Handler
}

// newHTTPHandler hands the endpoints the use cases they need as their own
// narrow interface.
func newHTTPHandler(app *application.Application, cfg accounthttp.Config, logger *slog.Logger) (*accounthttp.Handler, error) {
	return accounthttp.NewHandler(app, cfg, logger)
}

func register(r registration) {
	r.Mux.Handle(accounthttp.PathPrefix, r.Handler)
	path, handler := accountprivatev1connect.NewUserDirectoryServiceHandler(
		accountconnect.NewUserDirectory(r.App),
		connect.WithInterceptors(connectlog.NewInterceptor(r.Logger)),
	)
	r.Mux.Handle(path, handler)
	// The browser reads who is signed in under the gateway-verified identity.
	path, handler = accountpublicv1connect.NewSessionServiceHandler(accountconnect.Session{},
		connect.WithInterceptors(connectlog.NewInterceptor(r.Logger), authn.NewConnectUnaryInterceptor(authn.GatewayActorConfig{Enabled: true})))
	r.Mux.Handle(path, handler)
}

// Module wires Account into the controlplane process. The named MySQL
// connection "account" must be supplied by process composition.
var Module = fx.Module("domain.account",
	pkgmysql.Bind(Database),
	fx.Provide(newRuntime, fx.Private),
	fx.Provide(infrastructure.NewSigningKeys, fx.Private),
	fx.Provide(newGitHubIdentity, fx.Private),
	fx.Provide(newApplication, fx.Private),
	fx.Provide(newHTTPHandler, fx.Private),
	fx.Invoke(register),
	fx.Invoke(registerMaintenance),
)
