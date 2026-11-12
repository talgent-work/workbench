package http

import (
	"encoding/json"
	"errors"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"time"

	"github.com/talgent-work/workbench/internal/business/account/application"
	"github.com/talgent-work/workbench/internal/business/account/domain"
)

// tokenPath serves the OAuth 2.0 client credentials grant (RFC 6749 §4.4)
// to Applications; contracts docs/application-tokens.md is its contract.
const tokenPath = "/auth/token"

// maxTokenRequest bounds a grant request's body: a grant type and at most a
// client id and secret.
const maxTokenRequest = 4 << 10

// tokenError is an OAuth 2.0 error response (RFC 6749 §5.2).
type tokenError struct {
	status      int
	code        string
	description string
}

func (e tokenError) Error() string { return e.code + ": " + e.description }

func invalidRequest(description string) tokenError {
	return tokenError{http.StatusBadRequest, "invalid_request", description}
}

func (h *Handler) token(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	_, _, basic := r.BasicAuth()
	grant, err := readClientCredentialsGrant(w, r)
	if err != nil {
		writeTokenError(w, err, basic)
		return
	}
	issued, err := h.app.IssueApplicationIdentityToken(r.Context(), grant)
	switch {
	case errors.Is(err, domain.ErrClientSecretMismatch):
		writeTokenError(w, tokenError{http.StatusUnauthorized, "invalid_client", "the client is unknown or its secret does not match"}, basic)
		return
	case err != nil:
		h.logger.ErrorContext(r.Context(), "application identity token not issued", slog.Any("error", err))
		writeTokenError(w, tokenError{http.StatusServiceUnavailable, "temporarily_unavailable", "the token could not be issued; retry later"}, basic)
		return
	}
	expiresIn := int64(issued.ExpiresAt.Sub(h.cfg.Now()) / time.Second)
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": issued.Token,
		"token_type":   "Bearer",
		"expires_in":   max(expiresIn, 1),
	})
}

// readClientCredentialsGrant reads a form-encoded grant whose client
// authenticates with HTTP Basic or with credentials in the body, never both.
func readClientCredentialsGrant(w http.ResponseWriter, r *http.Request) (application.IssueApplicationIdentityToken, error) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/x-www-form-urlencoded" {
		return application.IssueApplicationIdentityToken{}, invalidRequest("the body must be application/x-www-form-urlencoded")
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxTokenRequest)
	if err := r.ParseForm(); err != nil {
		return application.IssueApplicationIdentityToken{}, invalidRequest("the body is not a valid form")
	}
	form := r.PostForm
	for name, values := range form {
		if len(values) > 1 {
			return application.IssueApplicationIdentityToken{}, invalidRequest(name + " is repeated")
		}
	}
	switch form.Get("grant_type") {
	case "client_credentials":
	case "":
		return application.IssueApplicationIdentityToken{}, invalidRequest("grant_type is required")
	default:
		return application.IssueApplicationIdentityToken{}, tokenError{http.StatusBadRequest, "unsupported_grant_type", "only client_credentials is supported"}
	}
	if form.Has("scope") {
		return application.IssueApplicationIdentityToken{}, tokenError{http.StatusBadRequest, "invalid_scope", "scopes are not supported"}
	}
	user, password, basic := r.BasicAuth()
	inBody := form.Has("client_id") || form.Has("client_secret")
	switch {
	case basic && inBody:
		return application.IssueApplicationIdentityToken{}, invalidRequest("use one client authentication method")
	case basic:
		// RFC 6749 §2.3.1: each part is form-encoded before Basic encoding.
		clientID, idErr := url.QueryUnescape(user)
		secret, secretErr := url.QueryUnescape(password)
		if idErr != nil || secretErr != nil {
			return application.IssueApplicationIdentityToken{}, tokenError{http.StatusUnauthorized, "invalid_client", "the client credentials are malformed"}
		}
		return application.IssueApplicationIdentityToken{ClientID: clientID, ClientSecret: secret}, nil
	case form.Get("client_id") == "" || form.Get("client_secret") == "":
		return application.IssueApplicationIdentityToken{}, tokenError{http.StatusUnauthorized, "invalid_client", "client authentication is required"}
	default:
		return application.IssueApplicationIdentityToken{ClientID: form.Get("client_id"), ClientSecret: form.Get("client_secret")}, nil
	}
}

func writeTokenError(w http.ResponseWriter, err error, basic bool) {
	var failure tokenError
	if !errors.As(err, &failure) {
		failure = tokenError{http.StatusServiceUnavailable, "temporarily_unavailable", "the token could not be issued; retry later"}
	}
	if failure.status == http.StatusUnauthorized && basic {
		w.Header().Set("WWW-Authenticate", `Basic realm="talgent"`)
	}
	writeJSON(w, failure.status, map[string]string{"error": failure.code, "error_description": failure.description})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
