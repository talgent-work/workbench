package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	sdk "github.com/alibaba/OpenSandbox/sdks/sandbox/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure/execution"
	internalpkg "github.com/talgent-work/workbench/internal/pkg"
	"github.com/talgent-work/workbench/internal/pkg/opensandbox"
	"go.uber.org/fx"
)

func TestExecutionCompositionReachesHTTP1OpenSandbox(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "GET", r.Method)
		assert.Equal(t, "/v1/sandboxes/11111111-1111-4111-8111-111111111111", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"code":"SANDBOX_NOT_FOUND","message":"not found"}`))
	}))
	defer server.Close()
	dir := t.TempDir()
	templates, configurations := filepath.Join(dir, "templates.yml"), filepath.Join(dir, "configurations.yml")
	require.NoError(t, os.WriteFile(templates, []byte("templates:\n  - {id: pro, vendor: deepseek, base-url: https://model.example/anthropic, opus-model: deployment-pro, sonnet-model: deployment-pro, haiku-model: deployment-flash, fable-model: deployment-pro, subagent-model: deployment-flash, thinking-levels: [low, high, max], default-thinking-level: max}\n  - {id: flash, vendor: deepseek, base-url: https://model.example/anthropic, opus-model: deployment-flash, sonnet-model: deployment-flash, haiku-model: deployment-flash, fable-model: deployment-flash, subagent-model: deployment-flash}\n"), 0600))
	require.NoError(t, os.WriteFile(configurations, []byte("configurations:\n  - {id: pro, name: Pro, template: pro, api-key: sentinel}\n  - {id: flash, name: Flash, template: flash, api-key: sentinel}\n"), 0600))
	cfg := ExecutionConfig{
		Environment: map[string]string{"TALGENT_API_ADDRESS": "http://controlplane.talgent.svc.cluster.local"},
		Models:      execution.ModelCatalogConfig{TemplatesPath: templates, ConfigurationsPath: configurations},
		OpenSandbox: opensandbox.Config{Address: server.URL + "/v1", APIKey: "test-key"},
		Native:      execution.NativeLaunchConfig{Binary: "claude"},
		Dispatcher:  ExecutionClientConfig{Address: "http://dispatcher:8083", Token: "test-control"},
		Image:       "test-native:fixed", SandboxNamespace: "talgent",
		Requests: map[string]string{"cpu": "250m", "memory": "512Mi"},
		Limits:   map[string]string{"cpu": "2", "memory": "2Gi"},
	}
	var sandboxes *execution.Sandboxes
	var models query.ModelConfigurations
	app := fx.New(fx.NopLogger, fx.Supply(cfg), fx.Provide(internalpkg.NewH2CClient, newExecutionLifecycle, func(cfg ExecutionConfig) (*execution.ModelCatalog, error) {
		return execution.LoadModelCatalog(cfg.Models)
	}, newExecutionSandboxes, newModelConfigurations), fx.Populate(&sandboxes, &models))
	require.NoError(t, app.Err())
	provided := models.Provided()
	require.Len(t, provided, 2)
	require.Equal(t, query.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "pro"}, Name: "Pro", Vendor: "deepseek", ThinkingLevels: []string{"low", "high", "max"}, DefaultThinkingLevel: "max"}, provided[0])
	require.Empty(t, provided[1].ThinkingLevels, "a template without declared levels offers none")
	defaultModel, ok := models.Default()
	require.True(t, ok)
	require.Equal(t, "pro", defaultModel.Selection.ConfigurationID)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	deleted, err := sandboxes.Delete(ctx, execution.SandboxOwner{WorkID: "session", ID: "01900000-0000-7000-8000-000000000001"}, sdk.CreateSandboxRequest{}, "11111111-1111-4111-8111-111111111111", nil)
	require.NoError(t, err, "production wiring must use the lifecycle server's HTTP/1.1 protocol")
	require.True(t, deleted)
}

// Sandboxes are leased for two hours and renewed below one unless configured
// otherwise.
func TestExecutionLeaseDefaultsAndConfiguration(t *testing.T) {
	require.Equal(t, domain.LeaseTerms{Duration: 2 * time.Hour, RenewalThreshold: time.Hour}, ExecutionLeaseConfig{}.terms())
	configured := ExecutionLeaseConfig{Duration: Duration(4 * time.Hour), RenewalThreshold: Duration(90 * time.Minute)}
	require.Equal(t, domain.LeaseTerms{Duration: 4 * time.Hour, RenewalThreshold: 90 * time.Minute}, configured.terms())
}
