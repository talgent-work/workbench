package execution

import (
	"testing"
	"time"

	sdk "github.com/alibaba/OpenSandbox/sdks/sandbox/go"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure/platformcatalog"
)

func TestCredentialPlanConfinesServicesAndKeepsModelWhenUnavailable(t *testing.T) {
	model := modelCredential{Endpoint: "https://model.example/anthropic", Key: "model-token"}
	now := time.Now()
	base, err := credentialPlan(model, nil, nil, now)
	require.NoError(t, err)
	require.Len(t, base.Credentials, 1)
	services := []command.ServiceAccessMaterial{
		{ServiceRequirement: command.ServiceRequirement{Provider: "github", Service: "github", Target: "github-api"}, AuthorizationID: "auth", ServiceCredentialMaterial: command.ServiceCredentialMaterial{Token: "provider-token", ExpiresAt: now.Add(time.Hour)}},
		{ServiceRequirement: command.ServiceRequirement{Provider: "github", Service: "github", Target: "github-remote-mcp"}, AuthorizationID: "auth", ServiceCredentialMaterial: command.ServiceCredentialMaterial{Token: "provider-token", ExpiresAt: now.Add(time.Hour)}},
	}
	plan, err := credentialPlan(model, nil, services, now)
	require.NoError(t, err)
	require.Len(t, plan.Credentials, 3)
	require.Len(t, plan.Bindings, 5)
	require.Equal(t, []string{"api.github.com"}, plan.Bindings[1].Match.Hosts)
	for _, binding := range plan.Bindings[2:] {
		require.Equal(t, []string{"api.githubcopilot.com"}, binding.Match.Hosts)
		require.Equal(t, []string{"/mcp", "/mcp/"}, binding.Match.Paths)
	}
	services[0].Target = "https://other.example"
	_, err = credentialPlan(model, nil, services, now)
	require.Error(t, err)
}

func TestNativeServiceEnvironmentContainsOnlyPublicPlaceholders(t *testing.T) {
	provider, _, fixture := sandboxClient(t)
	_, err := provider.submitRequest(t.Context(), request(), "execution-token")
	require.NoError(t, err)
	for _, name := range []string{"GH_TOKEN", "GITHUB_TOKEN", "GITHUB_PERSONAL_ACCESS_TOKEN"} {
		require.Equal(t, "opensandbox-credential-proxy", fixture.create.Env[name])
		cfg := config()
		cfg.Environment = map[string]string{name: "real-provider-token"}
		require.Error(t, cfg.Validate(), "deployment configuration must not bypass trusted supply")
	}
}

func TestWithdrawnModelConfigurationKeepsTheDeliveredModelCredential(t *testing.T) {
	now := time.Now()
	delivered, err := credentialPlan(modelCredential{Endpoint: "https://model.example/anthropic", Key: "model-token"}, nil, nil, now)
	require.NoError(t, err)
	state := &sdk.CredentialVaultState{Revision: 3}
	for _, credential := range delivered.Credentials {
		state.Credentials = append(state.Credentials, sdk.CredentialMetadata{Name: credential.Name, SourceType: "inline"})
	}
	for _, binding := range delivered.Bindings {
		match, auth := binding.Match, sdk.CredentialAuthMetadata{Type: string(binding.Auth.Type), Name: binding.Auth.Credential}
		state.Bindings = append(state.Bindings, sdk.CredentialBindingMetadata{Name: binding.Name, Match: &match, Auth: &auth})
	}

	withdrawn := modelCredential{Endpoint: "https://model.example/anthropic"}
	refresh, err := credentialPlan(withdrawn, nil, nil, now)
	require.NoError(t, err)
	require.Empty(t, refresh.Credentials, "no key is supplied for a withdrawn configuration")
	patch := vaultReplacement(state, refresh, retained(withdrawn))
	require.Empty(t, patch.Credentials.Delete, "the running Work keeps its delivered model credential")
	require.Empty(t, patch.Bindings.Delete)
	require.True(t, vaultMatches(state, refresh, retained(withdrawn)))
	require.False(t, vaultMatches(state, refresh, nil), "without retention the model entries would be unexpected")
}

func TestRenewalSuppliesTheCurrentKeyOnlyForTheSandboxEndpoint(t *testing.T) {
	deepseek := command.ModelTemplate{ID: "deepseek-flash", Vendor: "deepseek", BaseURL: "https://api.deepseek.com/anthropic", OpusModel: "a", SonnetModel: "a", HaikuModel: "a", FableModel: "a", SubagentModel: "a"}
	glm := command.ModelTemplate{ID: "glm", Vendor: "zhipu", BaseURL: "https://open.bigmodel.cn/api/anthropic", OpusModel: "b", SonnetModel: "b", HaikuModel: "b", FableModel: "b", SubagentModel: "b"}
	catalog := func(template, key string) *platformcatalog.Catalog {
		models, err := platformcatalog.NewCatalog([]command.ModelTemplate{deepseek, glm}, []command.ModelConfiguration{{ID: "work", Name: "Work", Template: template, APIKey: key}})
		require.NoError(t, err)
		return models
	}
	work := domain.WorkModelSelection{ConfigurationID: "work"}
	require.Equal(t, modelCredential{Endpoint: deepseek.BaseURL, Key: "rotated"}, renewalCredential(catalog("deepseek-flash", "rotated"), work, deepseek.BaseURL), "a rotated key reaches the running Work")
	require.Empty(t, renewalCredential(catalog("glm", "glm-key"), work, deepseek.BaseURL).Key, "another vendor's key never reaches this endpoint")
	require.Empty(t, renewalCredential(catalog("deepseek-flash", "key"), domain.WorkModelSelection{ConfigurationID: "withdrawn"}, deepseek.BaseURL).Key)
}
