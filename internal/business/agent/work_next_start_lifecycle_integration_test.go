//go:build integration

package agent_test

import (
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/stretchr/testify/require"
	agentprivatev1 "github.com/talgent-work/contracts/gen/go/agent/private/v1"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// Only the fixed owner (or a caller acting on its behalf) resumes a Stopped
// Work or restarts a Failed one; neither another participant nor the
// AgentDefinition may, and a refused request begins no execution.
func TestOnlyTheOwnerResumesOrRestarts(t *testing.T) {
	m := newLifecycleModule(t)
	definition := seedDefinition(t, m.db)
	stopped := seedWork(t, m.db, definition, workSeed{subject: "stopped", state: domain.WorkStopped}).ID
	failed := seedWork(t, m.db, definition, workSeed{subject: "failed", state: domain.WorkFailed}).ID
	m.start()
	for _, stranger := range []string{"employee-b", definition.ID} {
		require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(m.resume(stranger, stopped)), "%s is not the Work owner", stranger)
		require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(m.restartWork(stranger, failed)), "Restart is admitted only for the owner, not %s", stranger)
	}
	require.Equal(t, agentprivatev1.WorkState_WORK_STATE_STOPPED, m.state(stopped))
	require.Equal(t, agentprivatev1.WorkState_WORK_STATE_FAILED, m.state(failed))
	require.Len(t, launches(t, m.db), 2)
}

// The owner's Resume of a Stopped Work continues its Harness Session and its
// Restart of a Failed Work recovers it; either start keeps the Work's
// capabilities and the layout located at its creation.
func TestNextStartKeepsTheWorkContext(t *testing.T) {
	m := newLifecycleModule(t)
	definition := seedDefinition(t, m.db)
	cases := []struct {
		state domain.WorkState
		start func(subject, workID string) error
		mode  domain.ContextMode
	}{
		{state: domain.WorkStopped, start: m.resume, mode: domain.ContextContinue},
		{state: domain.WorkFailed, start: m.restartWork, mode: domain.ContextRecover},
	}
	works := map[domain.WorkState]seededWork{}
	for _, c := range cases {
		works[c.state] = seedWork(t, m.db, definition, workSeed{subject: string(c.state), state: c.state})
	}
	m.start()
	for _, c := range cases {
		t.Run(string(c.state), func(t *testing.T) {
			work := works[c.state]
			require.NoError(t, c.start(workOwner, work.ID))
			got := m.get(work.ID)
			require.Equal(t, agentprivatev1.WorkState_WORK_STATE_STARTING, got.State)
			require.Equal(t, work.HarnessSessionReference, got.ContextReference)
			next := m.runs.await(t, m.ctx, work.ID, func(e *domain.AgentExecution) bool { return e.ID != work.execution.ID }, "the admitted start begins its execution").Launch
			require.Equal(t, c.mode, next.ContextMode)
			require.Equal(t, work.HarnessSessionReference, next.HarnessSessionReference)
			require.Equal(t, work.LocatedLayout, next.LocatedLayout, "every start mounts the layout located at creation")
			require.Equal(t, work.ID, next.WorkID)
			require.Equal(t, workOwner, next.RepresentedPerson)
			require.Equal(t, workPrompt, next.Capabilities.Prompt)
			require.Equal(t, workStartupAgent, next.Capabilities.StartupAgent)
		})
	}
}
