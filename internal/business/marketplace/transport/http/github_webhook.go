package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

// GitHubWebhookPath is a configured external callback, retained through the BC move.
const GitHubWebhookPath = "/api/scm/github/webhook"

type GitHubWebhookApplication interface {
	HandleGitHubAppAuthorizationRevoked(context.Context, application.GitHubAppAuthorizationRevokedInput) error
	HandleGitHubInstallationChanged(context.Context, application.GitHubInstallationChangedInput) error
}

type WebhookHandler struct {
	secret string
	app    GitHubWebhookApplication
	logger *slog.Logger
}

func NewWebhookHandler(secret string, app GitHubWebhookApplication, logger *slog.Logger) (*WebhookHandler, error) {
	if app == nil {
		return nil, fmt.Errorf("github webhook handler requires Marketplace application")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &WebhookHandler{
		secret: strings.TrimSpace(secret),
		app:    app,
		logger: logger.With("component", "marketplace.github.webhook"),
	}, nil
}

func (h *WebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if h.secret == "" {
		http.Error(w, "github webhook is not configured", http.StatusServiceUnavailable)
		return
	}
	event := strings.TrimSpace(r.Header.Get("X-GitHub-Event"))
	delivery := strings.TrimSpace(r.Header.Get("X-GitHub-Delivery"))
	signature := strings.TrimSpace(r.Header.Get("X-Hub-Signature-256"))
	if event == "" || delivery == "" || signature == "" {
		http.Error(w, "missing github webhook headers", http.StatusBadRequest)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read webhook body", http.StatusBadRequest)
		return
	}
	if !verifyWebhookSignature(h.secret, signature, body) {
		http.Error(w, "invalid github webhook signature", http.StatusUnauthorized)
		return
	}
	action, err := h.handleEvent(r.Context(), event, body)
	if err != nil {
		code := http.StatusInternalServerError
		if errors.Is(err, domain.ErrInvalidRequiredField) {
			code = http.StatusBadRequest
		}
		h.logger.WarnContext(r.Context(), "github webhook handling failed",
			slog.String("delivery_id", delivery),
			slog.String("event", event),
			slog.String("action", action),
			slog.Any("error", err),
		)
		http.Error(w, "github webhook handling failed", code)
		return
	}
	h.logger.InfoContext(r.Context(), "github webhook handled",
		slog.String("delivery_id", delivery),
		slog.String("event", event),
		slog.String("action", action),
	)
	w.WriteHeader(http.StatusNoContent)
}

func (h *WebhookHandler) handleEvent(ctx context.Context, event string, body []byte) (string, error) {
	var payload githubWebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("%w: decode github webhook: %v", domain.ErrInvalidRequiredField, err)
	}
	action := strings.TrimSpace(payload.Action)
	switch event {
	case "github_app_authorization":
		if action != "revoked" {
			return action, nil
		}
		if payload.Sender.ID == 0 {
			return action, domain.ErrInvalidRequiredField
		}
		return action, h.app.HandleGitHubAppAuthorizationRevoked(ctx, application.GitHubAppAuthorizationRevokedInput{
			ProviderAccountID: strconv.FormatInt(payload.Sender.ID, 10),
		})
	case "installation", "installation_repositories":
		// Every action changes what the installation reaches; none changes an
		// authorization's state.
		if payload.Installation.ID == 0 {
			return action, domain.ErrInvalidRequiredField
		}
		input := application.GitHubInstallationChangedInput{InstallationID: strconv.FormatInt(payload.Installation.ID, 10)}
		if payload.Sender.ID != 0 {
			input.SenderAccountID = strconv.FormatInt(payload.Sender.ID, 10)
		}
		return action, h.app.HandleGitHubInstallationChanged(ctx, input)
	default:
		return action, nil
	}
}

func verifyWebhookSignature(secret, header string, body []byte) bool {
	const prefix = "sha256="
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	got, err := hex.DecodeString(strings.TrimPrefix(header, prefix))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return hmac.Equal(got, mac.Sum(nil))
}

type githubWebhookPayload struct {
	Action       string               `json:"action"`
	Sender       githubWebhookAccount `json:"sender"`
	Installation struct {
		ID int64 `json:"id"`
	} `json:"installation"`
}

type githubWebhookAccount struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
}
