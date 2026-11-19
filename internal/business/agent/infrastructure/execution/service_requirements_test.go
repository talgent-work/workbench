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

// The Credential Proxy adds a Provider credential only to requests for its
// target's declared hosts, paths and methods: every binding names those and
// nothing else, and a target declaring no paths or methods takes them all.
func TestCredentialPlanConfinesServicesToTheirTargets(t *testing.T) {
	model := modelCredential{Endpoint: "https://model.example/anthropic", Key: "model-token"}
	now := time.Now()
	base, err := credentialPlan(model, nil, nil, now)
	require.NoError(t, err)
	require.Len(t, base.Credentials, 1)
	material := func(target string, hosts ...string) command.ServiceCredentialMaterial {
		return command.ServiceCredentialMaterial{Token: "token-for-" + target, ExpiresAt: now.Add(time.Hour), Hosts: hosts}
	}
	remoteMCP := material("github-remote-mcp", "api.githubcopilot.com")
	remoteMCP.Paths, remoteMCP.Methods = []string{"/mcp", "/mcp/"}, []string{"GET", "POST", "DELETE"}
	services := []command.ServiceAccessMaterial{
		{ServiceRequirement: command.ServiceRequirement{Provider: "github", Service: "github", Target: "github-api"}, AuthorizationID: "auth", ServiceCredentialMaterial: material("github-api", "api.github.com")},
		{ServiceRequirement: command.ServiceRequirement{Provider: "github", Service: "github", Target: "github-remote-mcp"}, AuthorizationID: "auth", ServiceCredentialMaterial: remoteMCP},
		{ServiceRequirement: command.ServiceRequirement{Provider: "example-sso", Service: "example-sso", Target: "example-docs"}, AuthorizationID: "sso", ServiceCredentialMaterial: material("example-docs", "docs.example.com", "*.svc.example.com")},
	}
	plan, err := credentialPlan(model, nil, services, now)
	require.NoError(t, err)
	require.Len(t, plan.Credentials, 4)
	require.Len(t, plan.Bindings, 4)
	matches := map[string]sdk.CredentialMatch{}
	for _, binding := range plan.Bindings[1:] {
		matches[binding.Auth.Credential] = binding.Match
	}
	every := []string{"GET", "POST", "PUT", "PATCH", "DELETE"}
	https := []sdk.CredentialScheme{sdk.CredentialSchemeHTTPS}
	require.Equal(t, map[string]sdk.CredentialMatch{
		"github-api":        {Schemes: https, Hosts: []string{"api.github.com"}, Methods: every, Paths: []string{"/*"}},
		"github-remote-mcp": {Schemes: https, Hosts: []string{"api.githubcopilot.com"}, Methods: []string{"GET", "POST", "DELETE"}, Paths: []string{"/mcp", "/mcp/"}},
		"example-docs":      {Schemes: https, Hosts: []string{"docs.example.com", "*.svc.example.com"}, Methods: every, Paths: []string{"/*"}},
	}, matches)

	for name, declared := range map[string][]string{"no hosts": nil, "bare wildcard": {"*"}, "top-level wildcard": {"*.com"}, "address": {"https://api.github.com"}} {
		changed := append([]command.ServiceAccessMaterial(nil), services...)
		changed[0].Hosts = declared
		_, err = credentialPlan(model, nil, changed, now)
		require.Error(t, err, name)
	}
	for name, declared := range map[string]command.ServiceCredentialMaterial{"relative path": {Paths: []string{"mcp"}}, "inner wildcard": {Paths: []string{"/*/mcp"}},
		"unknown method": {Methods: []string{"CONNECT"}}} {
		changed := append([]command.ServiceAccessMaterial(nil), services...)
		changed[1].Paths, changed[1].Methods = declared.Paths, declared.Methods
		_, err = credentialPlan(model, nil, changed, now)
		require.Error(t, err, name)
	}
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
