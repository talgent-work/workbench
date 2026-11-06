//go:build integration

package infrastructure_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/application/query/querytest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"github.com/talgent-work/workbench/internal/testsupport/computertest"
)

type processObservationBarrier struct{ beforeReturn func() }

func (processObservationBarrier) ObserveProcess(context.Context, string) (command.WorkProcessObservation, error) {
	return command.WorkProcessObservation{ProcessObservation: domain.ProcessObservation{State: domain.ProcessPending}}, nil
}

func (processObservationBarrier) IsCurrentProcess(context.Context, string, string) (bool, error) {
	return true, nil
}

func (p processObservationBarrier) AcknowledgeExit(context.Context, string, string) (bool, error) {
	p.beforeReturn()
	return true, nil
}

// An exit fact acknowledged for the previous process cannot
// be committed against a Work resumed while it was being applied.
func TestProcessObservationCannotReclaimConcurrentResume(t *testing.T) {
	f, id := runningInputSession(t)
	ctx := t.Context()
	factory := infrastructure.NewExecutionRequestsFactory(executionPort{})
	feedback := command.NewApplyExecutionFeedbackHandler(time.Now, f.sessions, exitedProcess, factory, &commandtest.InputBuffer{}, &commandtest.InputSettlements{})
	barrier := processObservationBarrier{beforeReturn: func() {
		require.NoError(t, command.NewStopWorkHandler(f.sessions, infrastructure.NewWorkAuthorizer(f.db), factory).Handle(ctx, command.WorkControl{SubjectID: "owner", WorkID: id, RequestID: uuid.NewString()}))
		require.NoError(t, feedback.Handle(ctx, command.ExecutionFeedback{ProcessID: "process", WorkID: id, Kind: command.ExecutionExited}))
		require.NoError(t, feedback.Handle(ctx, command.ExecutionFeedback{ProcessID: "process", WorkID: id, Kind: command.ComputeReclaimed}))
		resume := command.NewResumeWorkHandler(time.Now, f.sessions, f.agents, infrastructure.NewWorkAuthorizer(f.db), computertest.Checked(startupAuthority{}), factory, f.sessions, infrastructure.NewContinuationRequests(f.db, time.Now), querytest.ProvidedModels(), commandtest.UnchangedPlugins{})
		require.NoError(t, resume.Handle(ctx, command.StartWork{SubjectID: "owner", WorkID: id, RequestID: uuid.NewString()}))
		require.NoError(t, feedback.Handle(ctx, command.ExecutionFeedback{ProcessID: "process", WorkID: id, Kind: command.ExecutorPrepared}))
		require.NoError(t, feedback.Handle(ctx, command.ExecutionFeedback{ProcessID: "process", WorkID: id, Kind: command.AgentStarted}))
	}}
	port := &recoveryExecutionPort{}
	stale := command.NewApplyExecutionFeedbackHandler(time.Now, f.sessions, barrier, infrastructure.NewExecutionRequestsFactory(port), &commandtest.InputBuffer{}, &commandtest.InputSettlements{})
	require.ErrorIs(t, stale.Handle(ctx, command.ExecutionFeedback{ProcessID: "process", WorkID: id, Kind: command.ExecutionExited}), domain.ErrConcurrentModification)
	require.Empty(t, port.reclaims, "stale observation must not delete the resumed process")
	current, err := infrastructure.NewAgentWorkRepository(f.db, time.Now).Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, domain.WorkWorking, current.State)
	require.True(t, current.HoldsProcess())
}
