// Package http serves Account's browser sign-in endpoints under /auth. They
// are plain HTTP rather than Connect: they run before a token exists, follow
// OAuth redirect and cookie semantics, and the JWKS must be a plain GET the
// gateway can fetch.
package http

import (
	"context"
	"errors"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"time"

	"github.com/talgent-work/workbench/internal/business/account/application"
	"github.com/talgent-work/workbench/internal/business/account/domain"
)

const (
	// PathPrefix is where the shared mux mounts these endpoints.
	PathPrefix = "/auth/"

	AccessCookie  = "talgent-access"
	RefreshCookie = "talgent-refresh"
	loginCookie   = "talgent-login"

	refreshPath  = "/auth/refresh"
	callbackPath = "/auth/github/callback"
)

// Use cases the endpoints delegate to, one per request.
type Application interface {
	SignInURL(state, codeChallenge string) string
	CompleteGitHubSignIn(ctx context.Context, cmd application.CompleteGitHubSignIn) (application.SignedIn, error)
	RefreshLoginSession(ctx context.Context, cmd application.RefreshLoginSession) (application.SignedIn, error)
	SignOut(ctx context.Context, accessToken string) error
	PublishedKeys(ctx context.Context) ([]byte, error)
}

type Config struct {
	// CookieDomain is empty for a host-only cookie.
	CookieDomain string
	// LoginAttemptKey signs the in-progress sign-in cookie.
	LoginAttemptKey []byte
	// LoginAttemptTTL bounds how long GitHub may take to send the person back.
	LoginAttemptTTL time.Duration
	// CallbackURL is where GitHub returns a sign-in, on one entry's host.
	CallbackURL string
	// ReturnOrigins are the other entries a sign-in may return to.
	ReturnOrigins []string
	Now           func() time.Time
}

type Handler struct {
	app      Application
	cfg      Config
	attempts loginAttempts
	// targets are where a sign-in may return.
	targets returnTargets
	logger  *slog.Logger
	router  *http.ServeMux
}

func NewHandler(app Application, cfg Config, logger *slog.Logger) (*Handler, error) {
	if len(cfg.LoginAttemptKey) < 32 || cfg.LoginAttemptTTL <= 0 {
		return nil, errors.New("account auth endpoints: a 32-byte login attempt key and a positive attempt lifetime are required")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	targets, err := newReturnTargets(cfg.CallbackURL, cfg.ReturnOrigins)
	if err != nil {
		return nil, err
	}
	h := &Handler{app: app, cfg: cfg, attempts: loginAttempts{key: cfg.LoginAttemptKey, ttl: cfg.LoginAttemptTTL}, targets: targets, logger: logger}
	router := http.NewServeMux()
	router.HandleFunc("GET /auth/github/login", h.login)
	router.HandleFunc("GET "+callbackPath, h.callback)
	router.HandleFunc("POST "+refreshPath, h.refresh)
	router.HandleFunc("POST /auth/logout", h.logout)
	router.HandleFunc("GET /auth/jwks.json", h.jwks)
	h.router = router
	return h, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.router.ServeHTTP(w, r)
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	attempt, err := h.attempts.start(h.targets.from(r.Host, r.URL.Query().Get("next")), h.cfg.Now())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "sign-in attempt could not start", slog.Any("error", err))
		http.Error(w, "sign-in unavailable", http.StatusInternalServerError)
		return
	}
	value, err := h.attempts.encode(attempt)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "sign-in attempt could not be encoded", slog.Any("error", err))
		http.Error(w, "sign-in unavailable", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, h.cookie(loginCookie, value, callbackPath, h.cfg.LoginAttemptTTL))
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, h.app.SignInURL(attempt.State, codeChallenge(attempt.Verifier)), http.StatusFound)
}

// Sign-in failures the sign-in page explains. The Portal's FAILURES table in
// packages/workbench-ui/pages/login-page.tsx has one notice per code; change both.
const (
	failureDenied      = "denied"
	failureExpired     = "expired"
	failureEmail       = "email"
	failureUnavailable = "unavailable"
)

func (h *Handler) callback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	http.SetCookie(w, h.expired(loginCookie, callbackPath))
	cookie, err := r.Cookie(loginCookie)
	if err != nil {
		h.failSignIn(w, r, "/", failureExpired)
		return
	}
	attempt, err := h.attempts.decode(cookie.Value, h.cfg.Now())
	if err != nil {
		h.failSignIn(w, r, "/", failureExpired)
		return
	}
	attempt.Next = h.targets.resolve(attempt.Next)
	query := r.URL.Query()
	if query.Get("state") != attempt.State {
		h.failSignIn(w, r, attempt.Next, failureExpired)
		return
	}
	if reason := query.Get("error"); reason != "" {
		failure := failureUnavailable
		if reason == "access_denied" {
			failure = failureDenied
		} else {
			h.logger.WarnContext(r.Context(), "github refused the sign-in", slog.String("github_error", reason))
		}
		h.failSignIn(w, r, attempt.Next, failure)
		return
	}
	signedIn, err := h.app.CompleteGitHubSignIn(r.Context(), application.CompleteGitHubSignIn{
		Code:         query.Get("code"),
		CodeVerifier: attempt.Verifier,
		UserAgent:    r.UserAgent(),
	})
	switch {
	case errors.Is(err, application.ErrSignInExpired):
		h.failSignIn(w, r, attempt.Next, failureExpired)
		return
	case errors.Is(err, application.ErrGitHubEmailMissing):
		h.failSignIn(w, r, attempt.Next, failureEmail)
		return
	case err != nil:
		h.logger.ErrorContext(r.Context(), "github sign-in failed", slog.Any("error", err))
		h.failSignIn(w, r, attempt.Next, failureUnavailable)
		return
	}
	h.setSession(w, signedIn)
	http.Redirect(w, r, attempt.Next, http.StatusFound)
}

func (h *Handler) failSignIn(w http.ResponseWriter, r *http.Request, next, failure string) {
	page, path := h.targets.signInPage(next)
	query := url.Values{"error": {failure}}
	if path != "/" {
		query.Set("next", path)
	}
	http.Redirect(w, r, page+"?"+query.Encode(), http.StatusFound)
}

func (h *Handler) refresh(w http.ResponseWriter, r *http.Request) {
	if !jsonRequest(r) {
		http.Error(w, "application/json required", http.StatusUnsupportedMediaType)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	cookie, err := r.Cookie(RefreshCookie)
	if err != nil {
		h.clearSession(w)
		http.Error(w, "sign in again", http.StatusUnauthorized)
		return
	}
	signedIn, err := h.app.RefreshLoginSession(r.Context(), application.RefreshLoginSession{RefreshSecret: cookie.Value})
	switch {
	case errors.Is(err, domain.ErrRefreshSecretReused):
		h.logger.WarnContext(r.Context(), "replaced refresh secret presented; login session revoked")
		fallthrough
	case errors.Is(err, domain.ErrRefreshSecretUnknown), errors.Is(err, domain.ErrLoginSessionRevoked), errors.Is(err, domain.ErrLoginSessionExpired):
		h.clearSession(w)
		http.Error(w, "sign in again", http.StatusUnauthorized)
		return
	case err != nil:
		h.logger.ErrorContext(r.Context(), "login session refresh failed", slog.Any("error", err))
		http.Error(w, "refresh unavailable", http.StatusServiceUnavailable)
		return
	}
	h.setSession(w, signedIn)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	if !jsonRequest(r) {
		http.Error(w, "application/json required", http.StatusUnsupportedMediaType)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	cookie, err := r.Cookie(AccessCookie)
	if err != nil {
		h.clearSession(w)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	err = h.app.SignOut(r.Context(), cookie.Value)
	if err != nil && !errors.Is(err, application.ErrIdentityTokenInvalid) {
		// The session is still live; keep this device's cookies so signing
		// out can be retried rather than leaving a session nobody can end.
		h.logger.ErrorContext(r.Context(), "login session revocation failed", slog.Any("error", err))
		http.Error(w, "sign-out incomplete", http.StatusServiceUnavailable)
		return
	}
	h.clearSession(w)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) jwks(w http.ResponseWriter, r *http.Request) {
	body, err := h.app.PublishedKeys(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "identity token keys unavailable", slog.Any("error", err))
		http.Error(w, "keys unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write(body)
}

// jsonRequest admits only JSON bodies: with SameSite=Lax cookies, a
// cross-site form cannot send this content type without a CORS preflight.
func jsonRequest(r *http.Request) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && mediaType == "application/json"
}

func (h *Handler) setSession(w http.ResponseWriter, signedIn application.SignedIn) {
	// Both cookies live as long as the session; the token inside the access
	// cookie expires on its own and sign-out still needs it to name the session.
	lifetime := signedIn.SessionExpiresAt.Sub(h.cfg.Now())
	h.clearHostOnlySession(w)
	http.SetCookie(w, h.cookie(AccessCookie, signedIn.AccessToken, "/", lifetime))
	http.SetCookie(w, h.cookie(RefreshCookie, signedIn.RefreshSecret, refreshPath, lifetime))
}

func (h *Handler) clearSession(w http.ResponseWriter) {
	h.clearHostOnlySession(w)
	http.SetCookie(w, h.expired(AccessCookie, "/"))
	http.SetCookie(w, h.expired(RefreshCookie, refreshPath))
}

// clearHostOnlySession ends session cookies a browser kept from before the
// cookies moved to the parent domain: a host-only cookie of the same name
// would otherwise be sent beside the new one, and an already-rotated refresh
// secret revokes its Login Session when presented again. Remove once no
// session predating the parent domain can still refresh (after the 90-day
// absolute session lifetime).
func (h *Handler) clearHostOnlySession(w http.ResponseWriter) {
	if h.cfg.CookieDomain == "" {
		return
	}
	for _, cookie := range []*http.Cookie{h.expired(AccessCookie, "/"), h.expired(RefreshCookie, refreshPath)} {
		cookie.Domain = ""
		http.SetCookie(w, cookie)
	}
}

func (h *Handler) cookie(name, value, path string, lifetime time.Duration) *http.Cookie {
	return &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     path,
		Domain:   h.cfg.CookieDomain,
		MaxAge:   max(int(lifetime/time.Second), 1),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	}
}

func (h *Handler) expired(name, path string) *http.Cookie {
	return &http.Cookie{Name: name, Path: path, Domain: h.cfg.CookieDomain, MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode}
}
