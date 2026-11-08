//go:build integration

package infrastructure_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	agentquery "github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/application/query/querytest"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"github.com/talgent-work/workbench/internal/testsupport/computertest"
)

type continuationCapacity struct {
	err   error
	calls int
}

func (c *continuationCapacity) AdmitRoom(context.Context) error { c.calls++; return c.err }

func stoppedContinuationSession(t *testing.T) (workCreationFixture, string) {
	t.Helper()
	f := newWorkCreationFixture(t)
	ctx := context.Background()
	create := command.NewCreateWorkHandler(time.Now, f.agents, f.sessions, access.NewWorkAuthorizer(infrastructure.NewWorkAccessReader(f.db)), agentquery.NewModelConfigurations(agentquery.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "deployment-default"}}), commandtest.StartCatalog(&creationCatalog{}, creationConnections{}), computertest.Checked(startupAuthority{}), creationPreparation(f.db, agentquery.NewModelConfigurations(agentquery.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "deployment-default"}})))
	id, err := create.Handle(ctx, f.request)
	require.NoError(t, err)
	// The Work recovery its creation wakes hands off its initial task input.
	require.NoError(t, command.NewInputAcceptance(f.sessions, &commandtest.InputBuffer{}).HandOffInitialTaskInput(ctx, id))
	run := newRuns(t, f.db, f.sessions)
	run.ready(ctx, id)
	// A normal exit follows an admitted stop (Lifecycle observation).
	require.NoError(t, command.NewStopWorkHandler(f.sessions, access.NewWorkAuthorizer(infrastructure.NewWorkAccessReader(f.db)), executionsOf(f.db)).Handle(ctx, command.WorkControl{SubjectID: f.request.SubjectID, WorkID: id, RequestID: uuid.NewString()}))
	run.exit(ctx, id)
	run.release(ctx, id)
	s, err := f.sessions.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, domain.WorkStopped, s.State)
	return f, id
}

// A refused Resume answers its caller and retains nothing: the Work stays
// Stopped, a repeat of the same request asks its Computer again, and once
// room is available it is admitted under that request.
func TestRefusedResumeRetainsNothingAndARepeatAsksAgain(t *testing.T) {
	f, id := stoppedContinuationSession(t)
	ctx := context.Background()
	before, err := f.sessions.Get(ctx, id)
	require.NoError(t, err)
	capacity := &continuationCapacity{err: domain.ErrCapacityUnavailable}
	starts := []domain.LaunchSpecification{}
	started := startedWorks{AgentWorkRepository: f.sessions, onStart: func(r domain.LaunchSpecification) { starts = append(starts, r) }}
	handler := command.NewResumeWorkHandler(time.Now, started, f.agents, access.NewWorkAuthorizer(infrastructure.NewWorkAccessReader(f.db)), computertest.Checked(capacity), querytest.ProvidedModels(), commandtest.UnchangedPlugins{})
	request := command.StartWork{SubjectID: "owner", WorkID: id, RequestID: uuid.NewString()}
	foreign := request
	foreign.SubjectID = "another-owner"
	require.Error(t, handler.Handle(ctx, foreign))
	require.Zero(t, capacity.calls, "an unauthorized request never asks the Computer")
	require.ErrorIs(t, handler.Handle(ctx, request), domain.ErrCapacityUnavailable)
	retained, err := f.sessions.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, domain.WorkStopped, retained.State)
	require.Equal(t, before.HarnessProcess, retained.HarnessProcess)
	require.Equal(t, before.HarnessSessionReference, retained.HarnessSessionReference)
	require.Equal(t, before.Version, retained.Version, "a refusal for capacity changes nothing on the Work")
	require.Empty(t, starts)

	capacity.err = nil
	require.NoError(t, handler.Handle(ctx, request), "the repeat asks again")
	require.Equal(t, 2, capacity.calls)
	require.Len(t, starts, 1)
	require.Equal(t, domain.ContextContinue, starts[0].ContextMode)
	require.Equal(t, before.HarnessSessionReference, starts[0].HarnessSessionReference)
	require.Equal(t, before.WorkspaceReference, starts[0].WorkspaceReference)
	require.NoError(t, handler.Handle(ctx, request), "an admitted request answers its original admission")
	require.Equal(t, 2, capacity.calls, "a repeat whose Desk the current start holds asks nothing")
	require.Len(t, starts, 1)
	retained, err = f.sessions.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, domain.WorkStarting, retained.State)
	require.Equal(t, before.CapabilitySnapshot, retained.CapabilitySnapshot)
}

var errContinuationAckLost = errors.New("continuation commit acknowledgement lost")

type lostContinuationAdmission struct {
	*infrastructure.AgentWorkRepository
	lost bool
}

func (r *lostContinuationAdmission) Save(ctx context.Context, s *domain.AgentWork) error {
	err := r.AgentWorkRepository.Save(ctx, s)
	if err == nil && r.lost {
		r.lost = false
		return errContinuationAckLost
	}
	return err
}

// A Resume commits its start at once: when the commit's answer is lost, the
// repeat finds the start holding the Desk its request derives and answers it
// without asking the Computer again, and the Policies begin its execution.
func TestResumeRepeatAfterALostCommitAnswersTheAdmittedStart(t *testing.T) {
	f, id := stoppedContinuationSession(t)
	ctx := context.Background()
	capacity := &continuationCapacity{}
	starts := []domain.LaunchSpecification{}
	started := startedWorks{AgentWorkRepository: &lostContinuationAdmission{AgentWorkRepository: f.sessions, lost: true}, onStart: func(r domain.LaunchSpecification) { starts = append(starts, r) }}
	h := command.NewResumeWorkHandler(time.Now, started, f.agents, access.NewWorkAuthorizer(infrastructure.NewWorkAccessReader(f.db)), computertest.Checked(capacity), querytest.ProvidedModels(), commandtest.UnchangedPlugins{})
	request := command.StartWork{SubjectID: "owner", WorkID: id, RequestID: uuid.NewString()}
	require.ErrorIs(t, h.Handle(ctx, request), errContinuationAckLost)
	require.Empty(t, starts, "response loss preceded outbound delivery")
	admitted, err := f.sessions.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, domain.WorkStarting, admitted.State, "the lost commit admitted the start")

	require.NoError(t, h.Handle(ctx, request))
	require.Equal(t, 1, capacity.calls, "the repeat asks no room")
	require.Empty(t, starts, "the start was admitted by the lost commit, not again")
	recovery := command.NewRecoverWorkHandler(time.Now, f.sessions, &commandtest.InputBuffer{}, command.NewInputAcceptance(f.sessions, &commandtest.InputBuffer{}), &commandtest.InputSettlements{}, nil)
	require.NoError(t, recovery.Handle(ctx, id))
	intended := requireStartRequested(t, f.sessions, id)
	require.Equal(t, id, intended.WorkID)
	require.Equal(t, domain.ContextContinue, intended.ContextMode)
	begun := newRuns(t, f.db, f.sessions).latest(ctx, id)
	require.Equal(t, intended, launchOf(begun), "the Policies begin the accepted admission's execution")
	require.Equal(t, domain.ExecutionStarting, begun.State)
}

// A withdrawn Model Configuration refuses Resume with that reason and leaves
// the Work as it was; once the platform provides it again, the Work resumes.
func TestContinuationRefusesAWithdrawnModelConfigurationUntilItIsProvidedAgain(t *testing.T) {
	f, id := stoppedContinuationSession(t)
	ctx := context.Background()
	before, err := f.sessions.Get(ctx, id)
	require.NoError(t, err)
	starts := []domain.LaunchSpecification{}
	started := startedWorks{AgentWorkRepository: f.sessions, onStart: func(r domain.LaunchSpecification) { starts = append(starts, r) }}
	withdrawn := command.NewResumeWorkHandler(time.Now, started, f.agents, access.NewWorkAuthorizer(infrastructure.NewWorkAccessReader(f.db)), computertest.Checked(&continuationCapacity{}), agentquery.NewModelConfigurations(), commandtest.UnchangedPlugins{})
	request := command.StartWork{SubjectID: "owner", WorkID: id, RequestID: uuid.NewString()}
	require.ErrorIs(t, withdrawn.Handle(ctx, request), domain.ErrModelConfigurationWithdrawn)
	require.ErrorIs(t, withdrawn.Handle(ctx, request), domain.ErrModelConfigurationWithdrawn, "a repeat is refused for the same reason")
	require.Empty(t, starts)
	retained, err := f.sessions.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, domain.WorkStopped, retained.State)
	require.Equal(t, before.HarnessProcess, retained.HarnessProcess)

	provided := command.NewResumeWorkHandler(time.Now, started, f.agents, access.NewWorkAuthorizer(infrastructure.NewWorkAccessReader(f.db)), computertest.Checked(&continuationCapacity{}), querytest.ProvidedModels(), commandtest.UnchangedPlugins{})
	require.NoError(t, provided.Handle(ctx, command.StartWork{SubjectID: "owner", WorkID: id, RequestID: uuid.NewString()}))
	require.Len(t, starts, 1)
	require.Equal(t, before.WorkModel, starts[0].WorkModel)
}

// unreachableComputer refuses every run as its Managed Computer being away.
type unreachableComputer struct{ command.ComputerPort }

func (unreachableComputer) AdmitRun(context.Context, command.RunAdmission) (command.ComputerAnswer, error) {
	return command.ComputerAnswer{Kind: command.ComputerRejected, Rejection: domain.ErrComputerUnreachable}, nil
}

// A Resume refused because the Computer is unreachable starts and fails
// nothing: the Work's view keeps its earlier start's startup facts and
// says the Resume is requested on reconnection (Agent model: Capacity
// rejection state, Retry on reconnection).
func TestContinuationRefusedAsUnreachableLeavesNoFailedStartup(t *testing.T) {
	f, id := stoppedContinuationSession(t)
	ctx := context.Background()
	handler := command.NewResumeWorkHandler(time.Now, f.sessions, f.agents, access.NewWorkAuthorizer(infrastructure.NewWorkAccessReader(f.db)), unreachableComputer{computertest.Checked(&continuationCapacity{})}, querytest.ProvidedModels(), commandtest.UnchangedPlugins{})
	views := infrastructure.NewWorkQueryRepository(f.db)
	before, err := views.GetWorkView(ctx, id)
	require.NoError(t, err)
	require.False(t, before.ResumeOnReconnection)

	err = handler.Handle(ctx, command.StartWork{SubjectID: "owner", WorkID: id, RequestID: uuid.NewString()})
	require.ErrorIs(t, err, domain.ErrComputerUnreachable)
	view, err := views.GetWorkView(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "stopped", view.State)
	require.True(t, view.ResumeOnReconnection, "the refused Resume is owed on reconnection")
	require.NotNil(t, view.StartupProgress)
	require.NotEqual(t, "failed", view.StartupProgress.Status, "the refusal shows no failed startup")
	require.Equal(t, before.StartupProgress.RequestID, view.StartupProgress.RequestID)
	require.Equal(t, before.StartupProgress.Status, view.StartupProgress.Status)
	require.Equal(t, before.StartupProgress.Reason, view.StartupProgress.Reason)
}
