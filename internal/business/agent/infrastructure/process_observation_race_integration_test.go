//go:build integration

package infrastructure_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/application/query/querytest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"github.com/talgent-work/workbench/internal/testsupport/computertest"
)

// executionSaveBarrier runs beforeSave once, before the first execution it
// saves is written.
type executionSaveBarrier struct {
	domain.AgentExecutionRepository
	beforeSave func()
}

func (b *executionSaveBarrier) Save(ctx context.Context, execution *domain.AgentExecution) error {
	if before := b.beforeSave; before != nil {
		b.beforeSave = nil
		before()
	}
	return b.AgentExecutionRepository.Save(ctx, execution)
}

// An exit fact acknowledged for the previous process cannot be committed
// against a Work resumed while it was being applied: its retry rereads the
// execution it was read against, which already recorded the exit, and leaves
// the resumed one alone.
func TestProcessObservationCannotReclaimConcurrentResume(t *testing.T) {
	f, id := runningInputSession(t)
	ctx := t.Context()
	feedback := newRuns(t, f.db, f.sessions)
	previous := feedback.latest(ctx, id)
	barrier := &executionSaveBarrier{AgentExecutionRepository: feedback.executions, beforeSave: func() {
		require.NoError(t, command.NewStopWorkHandler(f.sessions, access.NewWorkAuthorizer(infrastructure.NewWorkQueryRepository(f.db).WorkOwners), executionsOf(f.db)).Handle(ctx, command.WorkControl{SubjectID: "owner", WorkID: id, RequestID: uuid.NewString()}))
		require.NoError(t, feedback.Handle(ctx, command.ExecutionFeedback{ProcessID: "process", WorkID: id, Kind: command.ExecutionExited}))
		feedback.release(ctx, id)
		resume := command.NewResumeWorkHandler(time.Now, f.sessions, f.agents, access.NewWorkAuthorizer(infrastructure.NewWorkQueryRepository(f.db).WorkOwners), computertest.Checked(startupAuthority{}), querytest.ProvidedModels(), commandtest.UnchangedPlugins{})
		require.NoError(t, resume.Handle(ctx, command.StartWork{SubjectID: "owner", WorkID: id, RequestID: uuid.NewString()}))
		require.NoError(t, feedback.Handle(ctx, command.ExecutionFeedback{ProcessID: "process", WorkID: id, Kind: command.ExecutorPrepared}))
		require.NoError(t, feedback.Handle(ctx, command.ExecutionFeedback{ProcessID: "process", WorkID: id, Kind: command.AgentStarted}))
	}}
	stale := command.NewApplyExecutionFeedbackHandler(time.Now, f.sessions, barrier, feedback.coordination, &commandtest.InputBuffer{}, &commandtest.InputSettlements{})
	require.NoError(t, stale.Handle(ctx, command.ExecutionFeedback{ProcessID: "process", WorkID: id, Kind: command.ExecutionExited}), "the exit is already recorded on its own execution")
	resumed, err := feedback.executions.Latest(ctx, id)
	require.NoError(t, err)
	require.NotEqual(t, previous.ID, resumed.ID)
	require.Equal(t, domain.ExecutionRunning, resumed.State, "stale observation must not end the resumed process")
	require.False(t, resumed.Process.Exited)
	require.False(t, resumed.Retired())
	current, err := infrastructure.NewAgentWorkRepository(f.db, time.Now).Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, domain.WorkWorking, current.State)
	require.True(t, current.HoldsProcess())
}
