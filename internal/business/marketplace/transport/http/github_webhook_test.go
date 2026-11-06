package httpapi

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/marketplace/application"
)

type webhookApplication struct {
	revocations   []application.GitHubAppAuthorizationRevokedInput
	installations []application.GitHubInstallationChangedInput
}

func (a *webhookApplication) HandleGitHubInstallationChanged(_ context.Context, in application.GitHubInstallationChangedInput) error {
	a.installations = append(a.installations, in)
	return nil
}

func (a *webhookApplication) HandleGitHubAppAuthorizationRevoked(_ context.Context, in application.GitHubAppAuthorizationRevokedInput) error {
	a.revocations = append(a.revocations, in)
	return nil
}

func TestGitHubWebhookAuthenticatesBeforeMappingProviderFacts(t *testing.T) {
	for _, tc := range []struct {
		name, event, body string
		validSignature    bool
		status            int
	}{
		{"revocation", "github_app_authorization", `{"action":"revoked","sender":{"id":12345,"login":"octocat"}}`, true, http.StatusNoContent},
		{"forged-revocation", "github_app_authorization", `{"action":"revoked","sender":{"id":12345}}`, false, http.StatusUnauthorized},
		// Installation changes prompt an observation of the reach; they never
		// revoke an authorization.
		{"repository-removal", "installation_repositories", `{"action":"removed","installation":{"id":42},"sender":{"id":12345},"repositories_removed":[{"id":987}]}`, true, http.StatusNoContent},
		{"installation-loss", "installation", `{"action":"suspend","installation":{"id":42},"sender":{"id":12345}}`, true, http.StatusNoContent},
		{"forged-installation", "installation", `{"action":"created","installation":{"id":42}}`, false, http.StatusUnauthorized},
		{"installation-without-id", "installation", `{"action":"created","installation":{}}`, true, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := &webhookApplication{}
			handler, err := NewWebhookHandler("test-secret", app, nil)
			require.NoError(t, err)
			body := []byte(tc.body)
			request := httptest.NewRequest(http.MethodPost, GitHubWebhookPath, bytes.NewReader(body))
			request.Header.Set("X-GitHub-Event", tc.event)
			request.Header.Set("X-GitHub-Delivery", "test-delivery")
			mac := hmac.New(sha256.New, []byte("test-secret"))
			_, _ = mac.Write(body)
			signature := "sha256=" + hex.EncodeToString(mac.Sum(nil))
			if !tc.validSignature {
				signature = "sha256=deadbeef"
			}
			request.Header.Set("X-Hub-Signature-256", signature)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			require.Equal(t, tc.status, response.Code)
			if tc.name == "repository-removal" || tc.name == "installation-loss" {
				require.Empty(t, app.revocations)
				require.Equal(t, []application.GitHubInstallationChangedInput{{InstallationID: "42", SenderAccountID: "12345"}}, app.installations)
				return
			}
			require.Empty(t, app.installations)
			if tc.name != "revocation" {
				require.Empty(t, app.revocations)
				return
			}
			require.Len(t, app.revocations, 1)
			require.Equal(t, "12345", app.revocations[0].ProviderAccountID)
		})
	}
}
