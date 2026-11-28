//go:build integration

package execution

import (
	"encoding/json"
	"testing"
	"time"

	sdk "github.com/alibaba/OpenSandbox/sdks/sandbox/go"
	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// A Cloud Work whose Model Template names no endpoint is created without the
// platform's model variables or credential placeholder, and its credential
// Vault binds the Work's own credentials but no model credential.
func TestACloudWorkWithoutAModelEndpointGetsNoModelCredential(t *testing.T) {
	ctx := t.Context()
	f := newNativeWorkLifecycle(t, ctx)
	f.provisioning.sandboxes.config.Models = harnessDefaultModels()
	f.provisioning.credentials.models = harnessDefaultModels()
	work, _ := f.createWithAuthorization(t, ctx, domain.CapabilitySnapshot{}, domain.WorkAuthorizationInput{OwnerID: "lifecycle-owner"})
	record, found, err := latestAllocation(ctx, f.db, work.ID)
	require.NoError(t, err)
	require.True(t, found)
	var spec sdk.CreateSandboxRequest
	require.NoError(t, json.Unmarshal(record.SpecificationJSON, &spec))
	for _, name := range harnessModelEnvironment {
		require.NotContains(t, spec.Env, name)
	}
	admitted, err := decodeStartRequest(record.RequestJSON)
	require.NoError(t, err)

	supply := command.CredentialSupply{IdentityToken: command.IdentityToken{Token: "agent-token", ExpiresAt: time.Now().Add(7 * 24 * time.Hour)}}
	delivered, err := f.provisioning.credentials.prepare(ctx, record, admitted, spec, supply, false, admitting)
	require.NoError(t, err)
	require.Empty(t, delivered.ModelDigest, "no model key is delivered")
	f.external.vault.mu.Lock()
	defer f.external.vault.mu.Unlock()
	var credentials, bindings []string
	for _, credential := range f.external.vault.state.Credentials {
		credentials = append(credentials, credential.Name)
	}
	for _, binding := range f.external.vault.state.Bindings {
		bindings = append(bindings, binding.Name)
	}
	require.Equal(t, []string{identityTokenVaultEntry}, credentials, "the Work's Identity Token, no model credential")
	for _, entry := range modelVaultEntries {
		require.NotContains(t, bindings, entry)
	}
}

// A Managed Work whose Model Template names no endpoint opens its
// environment without the model proxy or the platform's model variables, and
// its delivery carries no model credential, so the Harness uses whatever
// credential the Work's environment provides.
func TestAManagedWorkWithoutAModelEndpointGetsNoModelProxy(t *testing.T) {
	f := newManagedWork(t)
	f.provisioning.managed.models = harnessDefaultModels()
	input := managedRequest()
	input.ContextMode = domain.ContextFresh
	input.WorkModel = domain.WorkModelSelection{ConfigurationID: "default"}
	record := f.launch(t, f.begin(t, input).ID)
	require.True(t, record.CredentialsDelivered)
	require.Empty(t, record.ModelCredentialDigest)
	spec, err := decodeManagedSpecification(record)
	require.NoError(t, err)
	require.Empty(t, spec.ModelEndpoint)

	f.relay.mu.Lock()
	defer f.relay.mu.Unlock()
	require.False(t, f.relay.modelProxy, "no model proxy replaces the Work's own credential")
	for _, name := range harnessModelEnvironment {
		require.NotContains(t, f.relay.environment, name)
	}
	require.Equal(t, "medium", f.relay.environment["CLAUDE_CODE_EFFORT_LEVEL"])
	var body map[string]any
	require.NoError(t, json.Unmarshal(f.relay.credentials, &body))
	require.NotContains(t, body, "model")
	require.NotContains(t, body, "keep_model")
}
