//go:build integration

package infrastructure_test

import (
	"context"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/application/query/querytest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"github.com/talgent-work/workbench/internal/testsupport/computertest"
	"xorm.io/xorm"
)

// Exercises the actual Session/definition stores, save-before-delivery boundary,
// fresh connection recovery, and durable execution-history read model.
func TestUserWaitTimeoutRecoversFromStoredOriginAndRetainsStopReason(t *testing.T) {
	ctx := context.Background()
	dsn := mysqltest.Database(t)
	db, err := xorm.NewEngine("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	origin := time.Date(2026, 9, 11, 12, 0, 0, 123000000, time.UTC)
	now := origin
	clock := func() time.Time { return now }
	agents := infrastructure.NewAgentDefinitionRepository(db, clock)
	sessions := infrastructure.NewAgentWorkRepository(db, clock)
	definition, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"owner"}, AgentName: "Fixture Agent", Purpose: "work", UsageScope: domain.UsageScope{Kind: "employee", Reference: "owner"}})
	require.NoError(t, err)
	require.NoError(t, agents.Save(ctx, definition))
	run := newRunsAt(t, db, sessions, clock)
	id, err := command.NewCreateWorkHandler(time.Now, agents, sessions, access.NewWorkAuthorizer(infrastructure.NewWorkQueryRepository(db).WorkOwners), creationModels(), commandtest.StartCatalog(&creationCatalog{}, creationConnections{}), computertest.Checked(startupAuthority{}), creationPreparation(db, creationModels())).Handle(ctx, command.CreateWork{Subject: domain.WorkSubject{Kind: "case", ID: "case"}, ProjectID: "project", RequestID: uuid.NewString(), SubjectID: "owner", AgentID: definition.ID})
	require.NoError(t, err)
	feedback := func(kind command.ExecutionFeedbackKind, interaction, stop string) error {
		return run.Handle(ctx, command.ExecutionFeedback{ProcessID: "process", Kind: kind, WorkID: id, InteractionID: interaction, ObservationSequence: 1000, RequiredInteractionIDs: []string{interaction}})
	}
	require.NoError(t, feedback(command.ExecutorPrepared, "", ""))
	require.NoError(t, feedback(command.AgentStarted, "", ""))
	// The question pauses a running Turn.
	require.NoError(t, run.Handle(ctx, command.ExecutionFeedback{ProcessID: "process", Kind: command.TurnStarted, WorkID: id, ObservationSequence: 999}))
	require.NoError(t, feedback(command.UserInteractionRequired, "question", ""))
	now = origin.Add(20 * time.Minute)
	require.NoError(t, feedback(command.UserInteractionRequired, "question", ""))
	reconnect := func() {
		require.NoError(t, db.Close())
		db, err = xorm.NewEngine("mysql", dsn)
		require.NoError(t, err)
		agents = infrastructure.NewAgentDefinitionRepository(db, clock)
		sessions = infrastructure.NewAgentWorkRepository(db, clock)
		run = newRunsAt(t, db, sessions, clock)
	}
	reconnect()
	waiting, err := sessions.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, origin, waiting.HarnessProcess.WaitingSince, "reconnect and duplicate feedback must retain original deadline")
	ids, err := infrastructure.NewExecutionQueryRepository(db).ListWorkIDs(ctx, "", 100)
	require.NoError(t, err)
	require.Contains(t, ids, id, "existing production recovery scan includes the waiting execution")
	recover := func() error {
		return command.NewRecoverWorkHandler(clock, sessions, &commandtest.InputBuffer{}, command.NewInputAcceptance(sessions, &commandtest.InputBuffer{}), &commandtest.InputSettlements{}, nil).Handle(ctx, id)
	}
	now = origin.Add(30*time.Minute - time.Millisecond)
	require.NoError(t, recover())
	require.False(t, run.latest(ctx, id).ExitRequest.Requested(), "no stop before the deadline")
	now = origin.Add(30 * time.Minute)
	// An optimistic-lock rejection must not deliver a stop.
	err = command.NewRecoverWorkHandler(clock, rejectedTimeoutSave{sessions}, &commandtest.InputBuffer{}, command.NewInputAcceptance(rejectedTimeoutSave{sessions}, &commandtest.InputBuffer{}), &commandtest.InputSettlements{}, nil).Handle(ctx, id)
	require.ErrorIs(t, err, domain.ErrConcurrentModification)
	begun := run.latest(ctx, id)
	require.False(t, begun.ExitRequest.Requested())
	// The timeout admits the stop, which asks the process to exit only once
	// committed, cancelling the paused Turn first (Stop between Turns).
	require.NoError(t, recover())
	asked := run.latest(ctx, id)
	durable, err := infrastructure.NewAgentWorkRepository(db, clock).Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, domain.WorkStopping, durable.State)
	require.Equal(t, domain.StopReasonWaitingTimeout, durable.StopReason)
	require.Equal(t, durable.StopRequestReference, asked.ExitRequest.StopRequestID, "request must be committed before external stop")
	require.True(t, asked.ExitRequest.CancelWaitingTurn, "the paused Turn is cancelled before the process exits")
	stop := asked.ExitRequest.StopRequestID
	reconnect()
	require.NoError(t, recover())
	again := run.latest(ctx, id)
	require.Equal(t, stop, again.ExitRequest.StopRequestID, "a repeated recovery asks the same stop again")
	require.Equal(t, begun.ID, again.ID, "timeout recovery cannot start or resume execution")
	require.NoError(t, feedback(command.UserInteractionResolved, "question", ""))
	durable, err = sessions.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, domain.WorkStopping, durable.State)
	require.Equal(t, origin, durable.HarnessProcess.WaitingSince.UTC(), "the stopped Work still owes the answer")
	require.Equal(t, domain.StopReasonWaitingTimeout, durable.StopReason)
	require.NoError(t, feedback(command.ExecutionExited, "", stop))
	run.release(ctx, id)
	reconnect()
	durable, err = sessions.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, domain.WorkStopped, durable.State)
	originalContext := durable.HarnessSessionReference
	_, err = continuedRef(id, command.NewResumeWorkHandler(time.Now, sessions, agents, access.NewWorkAuthorizer(infrastructure.NewWorkQueryRepository(db).WorkOwners), computertest.Checked(startupAuthority{}), querytest.ProvidedModels(), commandtest.UnchangedPlugins{}).Handle(ctx, command.StartWork{RequestID: uuid.NewString(), SubjectID: "owner", WorkID: id}))
	require.NoError(t, err)
	durable, err = sessions.Get(ctx, id)
	require.NoError(t, err)
	require.Empty(t, durable.StopReason)
	require.Equal(t, originalContext, durable.HarnessSessionReference)
	events, err := infrastructure.NewWorkQueryRepository(db).ListWorkLifecycleEvents(ctx, id)
	require.NoError(t, err)
	var states []domain.WorkState
	for _, event := range events {
		states = append(states, event.State)
	}
	require.Subset(t, states, []domain.WorkState{domain.WorkStopping, domain.WorkStopped, domain.WorkStarting}, "the timeout stop and the later resume remain visible on the Work timeline")
}

type rejectedTimeoutSave struct{ domain.AgentWorkRepository }

func (rejectedTimeoutSave) Save(context.Context, *domain.AgentWork) error {
	return domain.ErrConcurrentModification
}
