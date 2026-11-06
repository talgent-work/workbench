package domain_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

func workingWork(t *testing.T) *domain.AgentWork {
	t.Helper()
	work := newSession(t)
	require.NoError(t, recoverStartup(context.Background(), work, &executionPort{}))
	require.NoError(t, work.ConfirmPrepared(context.Background(), time.Now(), nil))
	require.NoError(t, work.ConfirmStarted(context.Background(), time.Now(), nil))
	return work
}

func TestSubAgentsReplaceTheRecordedOnesAsAWhole(t *testing.T) {
	work := workingWork(t)
	first := []domain.SubAgent{{ID: "a", Type: "Explore", Task: "map the code", Status: domain.SubAgentRunning}}
	require.NoError(t, work.RecordSubAgents(10, first))
	first[0].Task = "mutated caller input"
	require.Equal(t, "map the code", work.SubAgents()[0].Task)

	both := []domain.SubAgent{
		{ID: "a", Type: "Explore", Task: "map the code", Status: domain.SubAgentCompleted},
		{ID: "b", Type: "Plan", Task: "plan it", Status: domain.SubAgentRunning},
	}
	require.NoError(t, work.RecordSubAgents(20, both))
	require.Equal(t, both, work.SubAgents())
	// An earlier position of the current process is a replay and changes nothing.
	require.NoError(t, work.RecordSubAgents(15, first))
	require.Equal(t, both, work.SubAgents())
	require.Equal(t, domain.WorkWorking, work.State, "SubAgents change no Work State")

	require.ErrorIs(t, work.RecordSubAgents(30, []domain.SubAgent{{ID: "c", Status: "paused"}}), domain.ErrInvalidWork)
	require.ErrorIs(t, work.RecordSubAgents(30, []domain.SubAgent{{Status: domain.SubAgentRunning}}), domain.ErrInvalidWork)
	require.Equal(t, both, work.SubAgents())
}

func TestSubAgentsEndWithTheProcess(t *testing.T) {
	work := workingWork(t)
	running := []domain.SubAgent{{ID: "a", Type: "general-purpose", Task: "count files", Status: domain.SubAgentRunning}}
	require.NoError(t, work.RecordSubAgents(10, running))
	require.NoError(t, work.RecordExit(context.Background(), domain.HarnessExit{ExitCode: 1}, &executionPort{}))
	require.Empty(t, work.SubAgents(), "an exit clears them")
	require.NoError(t, work.RecordSubAgents(20, running))
	require.Empty(t, work.SubAgents(), "an ended process reports none")

	lost := workingWork(t)
	require.NoError(t, lost.RecordSubAgents(10, running))
	require.NoError(t, lost.ReconcileProcess(context.Background(), domain.ProcessObservation{State: domain.ProcessEnvironmentLost, ComputeReclaimed: true}, &executionPort{}))
	require.Empty(t, lost.SubAgents(), "confirmed environment loss clears them")
}
