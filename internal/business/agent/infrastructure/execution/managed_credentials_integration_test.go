//go:build integration

package execution

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// stoppableSessions is the Work's state as credential delivery reads it:
// working until its stop is admitted.
type stoppableSessions struct {
	mu       sync.Mutex
	stopping bool
}

func (s *stoppableSessions) Get(_ context.Context, id string) (*domain.AgentWork, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopping {
		return &domain.AgentWork{ID: id, State: domain.WorkStopping, StopRequestReference: "stop-1", HarnessProcess: &domain.HarnessProcess{}}, nil
	}
	return &domain.AgentWork{ID: id, State: domain.WorkWorking, HarnessProcess: &domain.HarnessProcess{}}, nil
}

func (*stoppableSessions) Save(context.Context, *domain.AgentWork) error { return nil }

// A Managed Work on a Model Configuration gets its key and its Gateway
// credentials only through its machine's local proxy: they are delivered when
// its environment runs, a rotated key is delivered again, and an admitted stop
// withdraws them. Neither the key nor the Work credential is in the Work's
// environment, its recorded specification or its launch command.
func TestManagedWorkCredentialsReachOnlyTheLocalProxy(t *testing.T) {
	ctx := context.Background()
	f := newManagedWork(t)
	sessions := &stoppableSessions{}
	f.provisioning.credentials.works.sessions = sessions
	input := managedRequest()
	input.ContextMode = domain.ContextFresh
	input.WorkModel = domain.WorkModelSelection{ConfigurationID: "default"}
	input.InputSource = domain.WorkInputSource{Reference: "file-space/case-1", SpaceToken: "space-token"}
	require.NoError(t, f.port.StartExecution(ctx, input))
	require.NoError(t, submitPending(ctx, f.provisioning))
	require.NoError(t, connectLive(ctx, f.connections))
	record, _, err := latestAllocation(ctx, f.db, input.WorkID)
	require.NoError(t, err)
	require.True(t, record.SandboxReady)
	require.Equal(t, "harness-handle", record.ProcessID)
	spec, err := decodeManagedSpecification(record)
	require.NoError(t, err)
	require.Equal(t, "https://model.example/anthropic", spec.ModelEndpoint)

	f.relay.mu.Lock()
	delivered, environment, modelProxy := string(f.relay.credentials), f.relay.environment, f.relay.modelProxy
	f.relay.mu.Unlock()
	require.True(t, modelProxy, "a Model Configuration's model is always reached through the local proxy")
	require.JSONEq(t, `{"model":{"base_url":"https://model.example/anthropic","key":"`+fixtureModelKey+`"},
		"gateway":{"url":"`+managedGatewayAddress+`","work_id":"`+input.WorkID+`","work_credential":"`+spec.WorkCredential+`","space_token":"space-token"},
		"api":{"url":"`+managedAPIAddress+`"}}`, delivered)
	require.Equal(t, modelCredential{Key: fixtureModelKey}.digest(), record.ModelCredentialDigest)
	require.Zero(t, record.CredentialsExpireAt, "nothing delivered expires")
	require.Equal(t, "fixture-primary", environment["ANTHROPIC_DEFAULT_OPUS_MODEL"], "the Model Template's environment reaches the Work")
	for _, name := range []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY"} {
		require.NotContains(t, environment, name, "the local proxy sets the model endpoint and token per run")
	}
	encodedEnvironment, err := json.Marshal(environment)
	require.NoError(t, err)
	for _, secret := range []string{fixtureModelKey, spec.WorkCredential, "space-token"} {
		require.NotContains(t, string(encodedEnvironment), secret)
		require.NotContains(t, record.LaunchCommand, secret)
		f.relay.mu.Lock()
		for _, process := range f.relay.created {
			require.NotContains(t, process.Command, secret)
		}
		f.relay.mu.Unlock()
	}
	require.NotContains(t, string(record.SpecificationJSON), fixtureModelKey, "the key is never recorded")
	require.True(t, strings.Contains(record.LaunchCommand, "${TALGENT_PROXY_URL}/mcp"), "the Talgent Connector goes through the local proxy")

	// A rotated key reaches the running Work.
	rotated, err := NewModelCatalog([]ModelTemplate{fixtureModels().Provided()[0].Template}, []ModelConfiguration{{ID: "default", Name: "default", Template: "fixture-template", APIKey: "rotated-key"}})
	require.NoError(t, err)
	f.provisioning.managed.models = rotated
	authority, err := f.provisioning.authority(record)
	require.NoError(t, err)
	digest, err := authority.(credentialDelivery).modelDigest(record)
	require.NoError(t, err)
	require.NotEqual(t, record.ModelCredentialDigest, digest, "the sweep sees the rotation")
	require.NoError(t, f.recovery.DeliverCredentials(ctx, record.ID))
	f.relay.mu.Lock()
	require.Contains(t, string(f.relay.credentials), `"key":"rotated-key"`)
	require.Equal(t, 2, f.relay.deliveries)
	f.relay.mu.Unlock()
	record, _, err = latestAllocation(ctx, f.db, input.WorkID)
	require.NoError(t, err)
	require.Equal(t, digest, record.ModelCredentialDigest)

	// An admitted stop withdraws the credentials while the process reports
	// its exit.
	sessions.mu.Lock()
	sessions.stopping = true
	sessions.mu.Unlock()
	_, err = f.recovery.ReconcileAllocation(ctx, record.ID)
	require.NoError(t, err)
	f.relay.mu.Lock()
	defer f.relay.mu.Unlock()
	require.Nil(t, f.relay.credentials)
	require.Equal(t, 1, f.relay.forgotten)
}

// A Work on a Model Configuration the platform no longer provides at its
// first delivery is not handed to the machine without a model, which would
// leave it on the machine's own Claude login.
func TestManagedFirstDeliveryNeedsTheModelConfigurationsKey(t *testing.T) {
	ctx := context.Background()
	f := newManagedWork(t)
	input := managedRequest()
	input.ContextMode = domain.ContextFresh
	input.WorkModel = domain.WorkModelSelection{ConfigurationID: "default"}
	require.NoError(t, f.port.StartExecution(ctx, input))
	require.NoError(t, submitPending(ctx, f.provisioning))
	withdrawn, err := NewModelCatalog([]ModelTemplate{fixtureModels().Provided()[0].Template}, []ModelConfiguration{{ID: "other", Name: "other", Template: "fixture-template", APIKey: "other-key"}})
	require.NoError(t, err)
	f.provisioning.managed.models = withdrawn
	require.ErrorIs(t, connectLive(ctx, f.connections), errModelConfigurationNotProvided)
	record, _, err := latestAllocation(ctx, f.db, input.WorkID)
	require.NoError(t, err)
	require.False(t, record.SandboxReady)
	f.relay.mu.Lock()
	defer f.relay.mu.Unlock()
	require.Zero(t, f.relay.deliveries)
	require.Equal(t, 1, f.relay.forgotten, "a failed first delivery withdraws whatever the machine held")
}
