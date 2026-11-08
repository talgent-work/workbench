//go:build integration

package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"

	sdk "github.com/alibaba/OpenSandbox/sdks/sandbox/go"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// Exercise persisted Work selection, Application resolution, retained Marketplace
// relationship facts and real Vault management HTTP together. Only external supply and
// compute/Vault endpoints are controlled; this is not live GitHub acceptance.
func TestServiceVaultReconcilesCurrentCredentialsWithoutChangingWorkSelection(t *testing.T) {
	ctx := t.Context()
	f := newNativeWorkLifecycle(t, ctx)
	snapshot, err := domain.NewCapabilitySnapshot(domain.StartupCapabilityInput{Connectors: []domain.CatalogConnector{connectorcatalogtest.Required()}, RequiredPlugins: []string{"github"}}, []domain.PluginRelease{{
		Slug: "github", Source: "fixture", Version: "1", RetrievalRef: "archive:github", NativeEntry: []byte(`{"name":"github","version":"1"}`),
		MCPServers: []domain.MCPComponent{{Name: "remote", Relationship: domain.ConnectorProviderRelationship{ConnectorID: "registered-remote", Provider: "github", Target: "github-remote-mcp", Authentication: "credential_proxy", Status: "supported"}, Definition: []byte(`{"type":"http","url":"https://api.githubcopilot.com/mcp/"}`)}},
	}})
	require.NoError(t, err)
	authorizationID := uuid.NewString()
	work, _ := f.createWithAuthorization(t, ctx, snapshot, domain.WorkAuthorizationInput{OwnerUserID: "lifecycle-owner", Available: []domain.PersonalAuthorization{{OwnerUserID: "lifecycle-owner", AuthorizationID: authorizationID, Provider: "github", Service: "github", SupportedTargets: []string{"github-api"}}}})
	stored, err := f.works.Get(ctx, work.ID)
	require.NoError(t, err)
	selection := stored.AuthorizationSelection.Clone()
	supply := &currentServiceSupply{token: "initial-provider-token"}
	f.provisioning.credentials.services = application.NewServiceAccess(f.works, supply)
	writes := &vaultWrites{next: f.external.vault}
	endpoint := httptest.NewServer(writes)
	t.Cleanup(endpoint.Close)
	f.external.egress = endpoint.URL
	record, found, err := latestAllocation(ctx, f.db, work.ID)
	require.NoError(t, err)
	require.True(t, found)
	admitted, err := decodeStartRequest(record.RequestJSON)
	require.NoError(t, err)
	var spec sdk.CreateSandboxRequest
	require.NoError(t, json.Unmarshal(record.SpecificationJSON, &spec))

	// Each stage is the delivery a credential change fact wakes: renewal,
	// revocation, a required reconnection and reauthorization.
	for _, stage := range []struct{ token, reason string }{
		{"initial-provider-token", ""}, {"rotated-provider-token", ""},
		{"", "revoked"}, {"", "reconnect_required"}, {"reauthorized-provider-token", ""},
	} {
		supply.token, supply.reason = stage.token, stage.reason
		delivery, err := f.provisioning.credentials.prepare(ctx, record, admitted, spec, stage.token != "initial-provider-token", admitting)
		statuses, expiry := delivery.Statuses, delivery.ExpiresAt
		require.NoError(t, err)
		if stage.token == "" {
			require.True(t, expiry.IsZero(), "nothing delivered expires once the binding is removed")
		} else {
			require.False(t, expiry.IsZero(), "the delivered expiry is reported")
		}
		require.Len(t, statuses, 2)
		for _, status := range statuses {
			if stage.reason == "" {
				require.Equal(t, "credential_available", status.Status)
			} else {
				require.Equal(t, "unavailable", status.Status)
				require.Equal(t, stage.reason, status.Reason)
			}
		}
		body := writes.latest()
		require.Contains(t, body, fixtureModelKey, "model credential remains available at every stage")
		if stage.token != "" {
			require.Contains(t, body, stage.token, "current material must reach the actual Vault HTTP write")
		} else {
			require.NotContains(t, body, "provider-token")
			f.external.vault.mu.Lock()
			names := make([]string, 0, len(f.external.vault.state.Credentials))
			for _, credential := range f.external.vault.state.Credentials {
				names = append(names, credential.Name)
			}
			f.external.vault.mu.Unlock()
			require.Equal(t, []string{"model", workIDVaultEntry, workCredentialVaultEntry}, names, "the model and the Gateway identity, no Provider credential")
		}
		current, err := f.works.Get(ctx, work.ID)
		require.NoError(t, err)
		require.Equal(t, selection, current.AuthorizationSelection)
		require.Equal(t, domain.WorkStarting, current.State)
		projection, err := json.Marshal(statuses)
		require.NoError(t, err)
		allocation, _, err := latestAllocation(ctx, f.db, work.ID)
		require.NoError(t, err)
		persisted, err := json.Marshal(allocation)
		require.NoError(t, err)
		for _, raw := range [][]byte{projection, persisted, record.SpecificationJSON} {
			require.NotContains(t, string(raw), "provider-token")
		}
	}
	for _, request := range supply.received {
		require.Equal(t, "lifecycle-owner", request.OwnerUserID)
		require.Equal(t, authorizationID, request.AuthorizationID)
		require.Contains(t, []string{"github-api", "github-remote-mcp"}, request.Target)
	}
}

type currentServiceSupply struct {
	command.MarketplacePort
	token, reason string
	received      []command.ServiceCredentialRequest
}

func (s *currentServiceSupply) SupplyServiceCredential(_ context.Context, request command.ServiceCredentialRequest) (command.ServiceCredentialMaterial, error) {
	s.received = append(s.received, request)
	if s.reason != "" {
		return command.ServiceCredentialMaterial{}, &command.ServiceCredentialUnavailable{Reason: s.reason, Temporary: s.reason == "unavailable"}
	}
	return command.ServiceCredentialMaterial{Token: s.token, ExpiresAt: time.Now().Add(time.Hour)}, nil
}

// Marketplace reports revoked and reconnect-required authorizations as inactive.

type vaultWrites struct {
	mu     sync.Mutex
	body   string
	bodies []string
	next   http.Handler
}

func (v *vaultWrites) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/credential-vault" && (r.Method == http.MethodPost || r.Method == http.MethodPatch) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_ = r.Body.Close()
		r.Body = io.NopCloser(bytes.NewReader(body))
		v.mu.Lock()
		v.body = string(body)
		v.bodies = append(v.bodies, string(body))
		v.mu.Unlock()
	}
	v.next.ServeHTTP(w, r)
}

func (v *vaultWrites) latest() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.body
}

// writesWith counts the Vault writes that carried the material.
func (v *vaultWrites) writesWith(material string) int {
	v.mu.Lock()
	defer v.mu.Unlock()
	count := 0
	for _, body := range v.bodies {
		if strings.Contains(body, material) {
			count++
		}
	}
	return count
}

// Two Works sharing one GitHub authorization both receive whatever a change
// to it leaves: a renewed credential, or no binding once it is revoked, while
// both keep running. A change to another authorization touches neither.
func TestCredentialChangeRedeliversEveryWorkUsingTheAuthorization(t *testing.T) {
	ctx := t.Context()
	f := newNativeWorkLifecycle(t, ctx)
	snapshot, err := domain.NewCapabilitySnapshot(domain.StartupCapabilityInput{Connectors: []domain.CatalogConnector{connectorcatalogtest.Required()}, RequiredPlugins: []string{"github"}}, []domain.PluginRelease{{
		Slug: "github", Source: "fixture", Version: "1", RetrievalRef: "archive:github", NativeEntry: []byte(`{"name":"github","version":"1"}`),
		MCPServers: []domain.MCPComponent{{Name: "remote", Relationship: domain.ConnectorProviderRelationship{ConnectorID: "registered-remote", Provider: "github", Target: "github-remote-mcp", Authentication: "credential_proxy", Status: "supported"}, Definition: []byte(`{"type":"http","url":"https://api.githubcopilot.com/mcp/"}`)}},
	}})
	require.NoError(t, err)
	authorizationID := uuid.NewString()
	shared := domain.WorkAuthorizationInput{OwnerUserID: "lifecycle-owner", Available: []domain.PersonalAuthorization{{OwnerUserID: "lifecycle-owner", AuthorizationID: authorizationID, Provider: "github", Service: "github", SupportedTargets: []string{"github-api"}}}}
	// Each Work's execution owns its own sandbox.
	f.external.distinct = true
	first, _ := f.createWithAuthorization(t, ctx, snapshot, shared)
	second, _ := f.createWithAuthorization(t, ctx, snapshot, shared)
	supply := &currentServiceSupply{token: "first-provider-token"}
	f.provisioning.credentials.services = application.NewServiceAccess(f.works, supply)
	writes := &vaultWrites{next: f.external.vault}
	endpoint := httptest.NewServer(writes)
	t.Cleanup(endpoint.Close)
	f.external.egress = endpoint.URL
	var allocations []string
	for _, work := range []*domain.AgentWork{first, second} {
		allocations = append(allocations, f.run(t, ctx, work.ID, "process-"+work.ID).ID)
	}
	lookups := infrastructure.NewExecutionQueryRepository(f.db)
	markChanged := func(authorization string) []string {
		t.Helper()
		ids, err := lookups.ListExecutionsUsingAuthorization(ctx, authorization)
		require.NoError(t, err)
		marked, err := f.reconcile.MarkCredentialsChanged(ctx, ids)
		require.NoError(t, err)
		return marked
	}
	using := markChanged(authorizationID)
	require.ElementsMatch(t, allocations, using, "the index finds every Work using the authorization")
	require.Empty(t, markChanged(uuid.NewString()))

	for _, stage := range []struct{ token, reason string }{{"renewed-provider-token", ""}, {"", "reconnect_required"}, {"", "revoked"}, {"reauthorized-provider-token", ""}} {
		supply.token, supply.reason = stage.token, stage.reason
		if stage.token != "renewed-provider-token" {
			// Each later change is a fact of its own that marks both again.
			require.ElementsMatch(t, allocations, markChanged(authorizationID))
		}
		for _, allocation := range using {
			_, err := f.reconcile.Reconcile(ctx, allocation)
			require.NoError(t, err)
		}
		if stage.token != "" {
			require.Equal(t, 2, writes.writesWith(stage.token), "both Vaults receive the changed credential")
		} else {
			require.NotContains(t, writes.latest(), "provider-token", "a credential Marketplace cannot supply loses its binding")
		}
		for _, work := range []*domain.AgentWork{first, second} {
			current, err := f.works.Get(ctx, work.ID)
			require.NoError(t, err)
			require.Equal(t, domain.WorkStarting, current.State, "the Work keeps running")
		}
	}
	for _, allocation := range using {
		record, _, err := f.provisioning.get(ctx, allocation)
		require.NoError(t, err)
		require.Greater(t, record.CredentialsExpireAt, time.Now().UnixMilli(), "the delivered expiry is recorded for the sweep")
	}
	for _, stage := range []string{"renewed-provider-token", "reauthorized-provider-token"} {
		require.Equal(t, 2, writes.writesWith(stage), "each change is delivered once to each Vault, not again while current")
	}

	// A temporary supply failure keeps what was delivered and is retried.
	supply.token, supply.reason = "", "unavailable"
	marked := markChanged(authorizationID)
	require.ElementsMatch(t, allocations, marked)
	before := writes.writesWith("reauthorized-provider-token")
	for _, allocation := range marked {
		_, err := f.reconcile.Reconcile(ctx, allocation)
		require.ErrorIs(t, err, errSupplyTemporarilyUnavailable)
		record, _, err := f.provisioning.get(ctx, allocation)
		require.NoError(t, err)
		require.Equal(t, staleDelivery.UnixMilli(), record.CredentialsExpireAt, "the change stays due until it is delivered")
	}
	require.Contains(t, writes.latest(), "reauthorized-provider-token", "a temporary failure never removes a delivered credential")
	require.Equal(t, before, writes.writesWith("reauthorized-provider-token"))
}
