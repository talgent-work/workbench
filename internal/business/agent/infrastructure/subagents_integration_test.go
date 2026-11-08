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
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/application/query/querytest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"github.com/talgent-work/workbench/internal/testsupport/computertest"
	"xorm.io/xorm"
)

func TestSubAgentsSurviveDatabaseReconnectUntilTheProcessExits(t *testing.T) {
	f, id := stoppedContinuationSession(t)
	ctx := context.Background()
	ref, err := continuedRef(id, command.NewResumeWorkHandler(time.Now, f.sessions, f.agents, access.NewWorkAuthorizer(infrastructure.NewWorkQueryRepository(f.db).WorkOwners), computertest.Checked(startupAuthority{}), querytest.ProvidedModels(), commandtest.UnchangedPlugins{}).Handle(ctx, command.StartWork{SubjectID: "owner", WorkID: id, RequestID: uuid.NewString()}))
	require.NoError(t, err)
	feedback := newRuns(t, f.db, f.sessions)
	for _, kind := range []command.ExecutionFeedbackKind{command.ExecutorPrepared, command.AgentStarted} {
		require.NoError(t, feedback.Handle(ctx, command.ExecutionFeedback{ProcessID: "process", Kind: kind, WorkID: ref}))
	}
	update := command.ExecutionFeedback{ProcessID: "process", Kind: command.SubAgentsUpdated, WorkID: ref, ObservationSequence: 10,
		SubAgents: []domain.SubAgent{{ID: "a", Type: "Explore", Task: "map the code", Status: domain.SubAgentRunning}}}
	require.NoError(t, feedback.Handle(ctx, update))
	update.ObservationSequence = 20
	update.SubAgents = []domain.SubAgent{
		{ID: "a", Type: "Explore", Task: "map the code", Status: domain.SubAgentCompleted},
		{ID: "b", Task: "review", Status: domain.SubAgentRunning},
	}
	require.NoError(t, feedback.Handle(ctx, update))

	db, err := xorm.NewEngine("mysql", f.dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	views := infrastructure.NewWorkQueryRepository(db)
	read := func() query.WorkView {
		value, err := views.GetWorkView(ctx, id)
		require.NoError(t, err)
		return value
	}
	require.Equal(t, []query.SubAgent{
		{ID: "a", Type: "Explore", Task: "map the code", Status: "completed"},
		{ID: "b", Task: "review", Status: "running"},
	}, read().SubAgents)

	feedback = newRuns(t, db, infrastructure.NewAgentWorkRepository(db, time.Now))
	require.NoError(t, feedback.Handle(ctx, command.ExecutionFeedback{ProcessID: "process", Kind: command.ExecutionExited, WorkID: ref}))
	require.Empty(t, read().SubAgents, "they end with the process")
}
