//go:build integration

package execution

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// A Managed run's telemetry token reaches its machine beside the Work's
// environment, never in it, and writes only that run's telemetry: it answers
// the attribution recorded at its start, Computer included, while the run is
// live, and nothing for another Work, a forged token or once the run is
// retired.
func TestAManagedRunsTelemetryTokenAttributesOnlyThatRun(t *testing.T) {
	ctx := context.Background()
	f := newManagedWork(t)
	attributions, err := NewTelemetryAttributions(f.provisioning)
	require.NoError(t, err)
	input := managedRequest()
	input.ContextMode = domain.ContextFresh
	input.TraceAttributes = map[string]string{"talgent.agent_work_id": input.WorkID, "talgent.user_id": "person-1", "talgent.computer_id": "forged"}
	record := f.launch(t, f.begin(t, input).ID)
	spec, err := decodeManagedSpecification(record)
	require.NoError(t, err)
	require.NotEmpty(t, spec.Telemetry.Token)
	token := spec.Telemetry.Token
	require.NotEmpty(t, token)

	f.relay.mu.Lock()
	delivered, environment := f.relay.telemetry, f.relay.environment
	f.relay.mu.Unlock()
	require.Equal(t, map[string]string{"endpoint": managedTelemetryAddress, "token": token}, delivered)
	encoded, err := json.Marshal(environment)
	require.NoError(t, err)
	for _, secret := range []string{token, spec.WorkCredential} {
		require.NotContains(t, string(encoded), secret, "Talgent Computer adds the token to the Harness's environment itself")
	}
	require.Equal(t, "talgent.agent_work_id="+input.WorkID+",talgent.computer_id="+managedComputerID+",talgent.computer_name=Studio%20Mac%2C%202,talgent.user_id=person-1", environment["OTEL_RESOURCE_ATTRIBUTES"])

	attributed, err := attributions.RunTelemetryAttribution(ctx, input.WorkID, token)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"talgent.agent_work_id": input.WorkID, "talgent.user_id": "person-1", "talgent.computer_id": managedComputerID, "talgent.computer_name": managedComputerName}, attributed)

	other := managedRequest()
	other.WorkID, other.ContextMode = "other-session", domain.ContextFresh
	require.NoError(t, f.steps.reconcile(ctx, f.begin(t, other).ID))
	otherRecord, _, err := latestAllocation(ctx, f.db, other.WorkID)
	require.NoError(t, err)
	otherSpec, err := decodeManagedSpecification(otherRecord)
	require.NoError(t, err)
	require.NotEqual(t, token, otherSpec.Telemetry.Token, "each run has its own token")
	for _, rejected := range []struct{ work, token string }{
		{input.WorkID, otherSpec.Telemetry.Token}, {other.WorkID, token}, {input.WorkID, "forged"}, {input.WorkID, ""}, {"unknown", token},
	} {
		_, err := attributions.RunTelemetryAttribution(ctx, rejected.work, rejected.token)
		require.ErrorIs(t, err, query.ErrTelemetryTokenRejected, "%s with %q", rejected.work, rejected.token)
	}

	f.exit(t, record)
	_, err = attributions.RunTelemetryAttribution(ctx, input.WorkID, token)
	require.ErrorIs(t, err, query.ErrTelemetryTokenRejected, "the token is revoked with its run")
}
