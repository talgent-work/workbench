package command_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	agentquery "github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

func retainedPreparation(sessions *feedbackSessionStore) *initialRequestStore {
	input := command.PrepareWorkExecution{SubjectID: "owner", WorkID: sessions.session.ID, WorkspaceReference: sessions.session.WorkspaceReference, RequestID: uuid.NewString(), WorkBranch: "talgent/" + "TAL-368", Input: command.ExecutionInput{ID: uuid.NewString(), Text: "original task"}}
	return &initialRequestStore{request: command.InitialExecutionRequest{PrepareWorkExecution: input, Version: 1, Submitted: true}}
}

func TestPreparationUsesOnlyRetainedCreationRequest(t *testing.T) {
	_, sessions := startupStores(t)
	store := retainedPreparation(sessions)
	handler := command.NewPrepareWorkExecutionHandler(store, sessions, sessionAccess{}, nil, agentquery.ModelConfigurations{})
	input := store.request.PrepareWorkExecution
	unsupported := input
	unsupported.ThinkingLevel = "not-a-harness-level"
	_, err := handler.Handle(t.Context(), unsupported)
	require.ErrorIs(t, err, domain.ErrInvalidWork)
	changed := input
	changed.Input.Text = "replace the initial task"
	_, err = handler.Handle(t.Context(), changed)
	require.ErrorIs(t, err, command.ErrInitialExecutionConflict)
	require.False(t, store.request.Prepared)
	id, err := handler.Handle(t.Context(), input)
	require.NoError(t, err)
	require.Equal(t, input.WorkID, id)
	require.True(t, store.request.Prepared)
	require.True(t, store.request.Submitted)
	require.Equal(t, domain.WorkStarting, sessions.session.State)
	require.True(t, sessions.session.HoldsProcess())
	input.RequestID, input.Input.ID = uuid.NewString(), uuid.NewString()
	_, err = handler.Handle(t.Context(), input)
	require.NoError(t, err)
	require.Zero(t, sessions.saves, "preparation admits no start: the Work's admitted one is its execution's")
	require.Empty(t, sessions.admittedStarts(), "a retry reuses preparation without admitting a new start")
	require.NotEqual(t, input.Input.ID, store.request.Input.ID)
}

func TestPreparationCannotInventInitialRequest(t *testing.T) {
	_, sessions := startupStores(t)
	input := retainedPreparation(sessions).request.PrepareWorkExecution
	handler := command.NewPrepareWorkExecutionHandler(&initialRequestStore{}, sessions, sessionAccess{}, nil, agentquery.ModelConfigurations{})
	_, err := handler.Handle(t.Context(), input)
	require.ErrorIs(t, err, command.ErrInitialExecutionNotFound)
	require.Zero(t, sessions.saves)
}

func TestPreparationForClosedSessionDoesNotAcquireResources(t *testing.T) {
	_, sessions := startupStores(t)
	report(t, sessions.session, func(workID string, start time.Time) domain.Event {
		return domain.ExecutionExited{WorkID: workID, StartAdmittedAt: start, Exit: domain.HarnessExit{ExitCode: 1}}
	})
	report(t, sessions.session, releasedReport)
	require.NoError(t, sessions.session.Close("already-closed", domain.ClosureFacts{}))
	store := retainedPreparation(sessions)
	prepare := command.NewPrepareWorkExecutionHandler(store, sessions, sessionAccess{}, nil, agentquery.ModelConfigurations{})
	_, err := prepare.Handle(t.Context(), store.request.PrepareWorkExecution)
	require.ErrorIs(t, err, domain.ErrWorkTransition)
	require.Equal(t, domain.WorkClosed, sessions.session.State)
	require.False(t, store.request.Prepared)
	require.Zero(t, sessions.saves)
}

type initialRequestStore struct {
	request         command.InitialExecutionRequest
	deliverySaveErr error
}

func (s *initialRequestStore) Get(context.Context, string) (command.InitialExecutionRequest, error) {
	if s.request.WorkID == "" {
		return command.InitialExecutionRequest{}, command.ErrInitialExecutionNotFound
	}
	return s.request, nil
}
func (s *initialRequestStore) Save(_ context.Context, request command.InitialExecutionRequest) error {
	if request.InputBuffered && s.deliverySaveErr != nil {
		return s.deliverySaveErr
	}
	if s.request.Version != request.Version {
		return domain.ErrConcurrentModification
	}
	s.request = request
	s.request.Version++
	return nil
}
