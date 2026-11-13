package command_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	agentquery "github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/application/query/querytest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/testsupport/authorizationcatalogtest"
)

// deskLedger is a Computer that keeps its Desks: a repeated Desk identity is
// answered with that Desk's current answer, and a Desk that left can never
// seat a run again.
type deskLedger struct {
	capacityCheck
	mu       sync.Mutex
	asked    []string
	open     map[string]bool
	released []string
}

func (l *deskLedger) AdmitRun(ctx context.Context, run command.RunAdmission) (command.ComputerAnswer, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.asked = append(l.asked, run.DeskID)
	if open, known := l.open[run.DeskID]; known && !open {
		return command.ComputerAnswer{}, command.ErrDeskUnavailable
	}
	answer, err := l.capacityCheck.AdmitRun(ctx, run)
	if err == nil && answer.Kind == command.ComputerSeated {
		if l.open == nil {
			l.open = map[string]bool{}
		}
		l.open[run.DeskID] = true
	}
	return answer, err
}

func (l *deskLedger) ReleaseUnusedDesk(_ context.Context, deskID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.released = append(l.released, deskID)
	if _, known := l.open[deskID]; known {
		l.open[deskID] = false
	}
}

func creationFixture(t *testing.T, computer command.ComputerPort) (*sessionStore, *command.CreateWorkHandler, command.CreateWork) {
	t.Helper()
	agent, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"caller"}, Purpose: "work", UsageScope: domain.UsageScope{Kind: "employee", Reference: "employee"}})
	require.NoError(t, err)
	store := &sessionStore{}
	models := agentquery.NewModelConfigurations(agentquery.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "default"}})
	handler := command.NewCreateWorkHandler(time.Now, domain.BuiltInPlugins{}, agentStore{agent}, store, store, models, commandtest.StartCatalog(emptyPluginCatalog{}, authorizationcatalogtest.Empty{}), computer, store.preparation(models))
	request := command.CreateWork{AgentID: agent.ID, SubjectID: "caller", WorkCaller: domain.WorkCaller{Kind: domain.CallerKindPerson, ID: "employee"},
		InitialExecution: &command.PrepareWorkExecution{RequestID: uuid.NewString(), WorkBranch: "talgent/work", Input: command.ExecutionInput{ID: uuid.NewString(), Text: "Do the work"}}}
	return store, handler, request
}

// A repeated creation after a lost answer returns the Work its identity
// created, asks for no second Desk and creates no second Work.
func TestARepeatedCreationReturnsItsOriginalWork(t *testing.T) {
	computer := &deskLedger{}
	store, handler, request := creationFixture(t, computer)
	id, err := handler.Handle(t.Context(), request)
	require.NoError(t, err)
	require.Len(t, computer.asked, 1)
	initial, pending := store.session.PendingInitialTaskInput()
	require.True(t, pending, "the Work commits with its initial task input")
	require.Equal(t, request.InitialExecution.Input.ID, initial.ID)

	repeated, err := handler.Handle(t.Context(), request)
	require.NoError(t, err)
	require.Equal(t, id, repeated)
	require.Equal(t, 1, store.registrations, "one request identity creates one Work")
	require.Len(t, computer.asked, 1, "a repeat finds its Work and asks its Computer nothing")
	require.Empty(t, computer.released)
}

// A repeat of a request identity that supplies other Work facts than its
// Work fixed is refused as a Mismatched reuse, as a Work Key repeat is.
func TestARepeatWithOtherWorkFactsIsAMismatchedReuse(t *testing.T) {
	cases := map[string]func(*command.CreateWork){
		"another Work Caller": func(r *command.CreateWork) { r.WorkCaller = domain.WorkCaller{Kind: "case", ID: "case-1"} },
		"a Work Key":          func(r *command.CreateWork) { r.WorkKey = domain.WorkKey{Kind: "review", ID: "review-1"} },
		"a File Home":         func(r *command.CreateWork) { r.FileHome = domain.FileHome{Kind: "project", ID: "project-1"} },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			store, handler, request := creationFixture(t, &deskLedger{})
			_, err := handler.Handle(t.Context(), request)
			require.NoError(t, err)
			change(&request)
			_, err = handler.Handle(t.Context(), request)
			require.ErrorIs(t, err, domain.ErrMismatchedReuse)
			require.Equal(t, 1, store.registrations)
		})
	}
}

// A commit that never lands leaves its Desk to the repeat, which asks for
// the same Desk and commits the same Work; the open-Desk reconciliation
// vacates the Desk if no repeat follows.
func TestACreationWhoseCommitFailedSucceedsOnItsRepeat(t *testing.T) {
	computer := &deskLedger{}
	store, handler, request := creationFixture(t, computer)
	lost := errors.New("connection lost during the commit")
	store.commitErr = lost
	_, err := handler.Handle(t.Context(), request)
	require.ErrorIs(t, err, lost)
	require.Empty(t, computer.released, "an uncertain commit keeps its Desk")
	require.Nil(t, store.session, "nothing starts before the Work commits")

	id, err := handler.Handle(t.Context(), request)
	require.NoError(t, err)
	require.Equal(t, store.session.ID, id)
	require.Len(t, computer.asked, 2)
	require.Equal(t, computer.asked[0], computer.asked[1], "the repeat asks for the same Desk")
	require.Equal(t, computer.asked[0], store.session.HarnessProcess.DeskID)
}

// A creation whose commit landed but whose answer was lost converges on its
// repeat: the same Work, without a second Desk.
func TestACreationWhoseAnswerWasLostConvergesOnItsRepeat(t *testing.T) {
	computer := &deskLedger{}
	store, handler, request := creationFixture(t, computer)
	lost := errors.New("connection lost after the commit")
	store.answerErr = lost
	_, err := handler.Handle(t.Context(), request)
	require.ErrorIs(t, err, lost)

	id, err := handler.Handle(t.Context(), request)
	require.NoError(t, err)
	require.Equal(t, store.session.ID, id)
	require.Equal(t, 1, store.registrations)
	require.Len(t, computer.asked, 1, "the repeat finds the Work its identity derives")
}

// A repeat whose Desk already left without a committed Work can no longer
// create one.
func TestACreationWhoseDeskLeftCreatesNothing(t *testing.T) {
	computer := &deskLedger{}
	store, handler, request := creationFixture(t, computer)
	store.commitErr = errors.New("connection lost during the commit")
	_, err := handler.Handle(t.Context(), request)
	require.Error(t, err)
	computer.ReleaseUnusedDesk(t.Context(), computer.asked[0])

	_, err = handler.Handle(t.Context(), request)
	require.ErrorIs(t, err, command.ErrDeskUnavailable)
	require.Nil(t, store.session)
}

// A refused creation keeps nothing of its request: no record settles it, so
// its caller's repeat is admitted afresh once its Computer has room.
func TestARefusedCreationKeepsNothingOfItsRequest(t *testing.T) {
	computer := &deskLedger{capacityCheck: capacityCheck{err: domain.ErrCapacityUnavailable}}
	store, handler, request := creationFixture(t, computer)
	_, err := handler.Handle(t.Context(), request)
	require.ErrorIs(t, err, domain.ErrCapacityUnavailable)
	require.Nil(t, store.session)

	computer.err = nil
	id, err := handler.Handle(t.Context(), request)
	require.NoError(t, err)
	require.Equal(t, store.session.ID, id)
	require.Equal(t, computer.asked[0], computer.asked[1], "the repeat asks for the same Desk")
}

// A repeated Restart after a lost answer returns its original outcome without
// a second start or Desk; one whose commit failed keeps its Desk for the
// repeat, which asks for the same one.
func TestARepeatedRestartAsksForTheSameDesk(t *testing.T) {
	agents, sessions := failedStartupStores(t)
	computer := &deskLedger{}
	restart := command.NewRestartWorkHandler(time.Now, sessions, agents, sessionAccess{}, computer, querytest.ProvidedModels(), commandtest.UnchangedPlugins{})
	request := command.StartWork{RequestID: uuid.NewString(), SubjectID: "owner", WorkID: sessions.session.ID}

	lost := errors.New("connection lost during the commit")
	sessions.commitErr = lost
	_, err := restart.Handle(t.Context(), request)
	require.ErrorIs(t, err, lost)
	require.Empty(t, computer.released, "an uncertain commit keeps its Desk")
	require.Equal(t, domain.WorkFailed, sessions.session.State)

	admitted, err := restart.Handle(t.Context(), request)
	require.NoError(t, err)
	require.True(t, admitted)
	require.Equal(t, domain.WorkStarting, sessions.session.State)
	require.Len(t, computer.asked, 2)
	require.Equal(t, computer.asked[0], computer.asked[1], "the repeat asks for the same Desk")

	admitted, err = restart.Handle(t.Context(), request)
	require.NoError(t, err)
	require.True(t, admitted, "a repeat returns the original admission")
	require.Len(t, computer.asked, 2, "a repeat whose Desk the current start holds asks its Computer nothing")
	require.Len(t, sessions.admittedStarts(), 1, "one start reaches execution")
}

// A Resume whose Desk left without a committed start can no longer start.
func TestAResumeWhoseDeskLeftCannotStart(t *testing.T) {
	agents, sessions := startupStores(t)
	stopWork(t, sessions)
	computer := &deskLedger{}
	resume := command.NewResumeWorkHandler(time.Now, sessions, agents, sessionAccess{}, computer, querytest.ProvidedModels(), commandtest.UnchangedPlugins{})
	request := command.StartWork{RequestID: uuid.NewString(), SubjectID: "owner", WorkID: sessions.session.ID}
	lost := errors.New("connection lost during the commit")
	sessions.commitErr = lost
	require.ErrorIs(t, resume.Handle(t.Context(), request), lost)
	computer.ReleaseUnusedDesk(t.Context(), computer.asked[0])

	require.ErrorIs(t, resume.Handle(t.Context(), request), command.ErrDeskUnavailable)
	require.Equal(t, domain.WorkStopped, sessions.session.State)
}

// A late repeat of a Resume whose start already ended starts nothing: its
// Desk left with the start's compute, and Computer refuses it.
func TestALateRepeatOfAnEndedResumeStartsNothing(t *testing.T) {
	agents, sessions := startupStores(t)
	stopWork(t, sessions)
	computer := &deskLedger{}
	resume := command.NewResumeWorkHandler(time.Now, sessions, agents, sessionAccess{}, computer, querytest.ProvidedModels(), commandtest.UnchangedPlugins{})
	request := command.StartWork{RequestID: uuid.NewString(), SubjectID: "owner", WorkID: sessions.session.ID}
	require.NoError(t, resume.Handle(t.Context(), request))
	stopWork(t, sessions)
	computer.ReleaseUnusedDesk(t.Context(), computer.asked[0])

	require.ErrorIs(t, resume.Handle(t.Context(), request), command.ErrDeskUnavailable)
	require.Equal(t, domain.WorkStopped, sessions.session.State)
	require.Empty(t, sessions.admittedStarts())
}

// A Resume whose commit landed but whose answer was lost converges on its
// repeat to the same admission.
func TestAResumeWhoseAnswerWasLostConvergesOnItsRepeat(t *testing.T) {
	agents, sessions := startupStores(t)
	stopWork(t, sessions)
	computer := &deskLedger{}
	resume := command.NewResumeWorkHandler(time.Now, sessions, agents, sessionAccess{}, computer, querytest.ProvidedModels(), commandtest.UnchangedPlugins{})
	request := command.StartWork{RequestID: uuid.NewString(), SubjectID: "owner", WorkID: sessions.session.ID}
	lost := errors.New("connection lost after the commit")
	sessions.answerErr = lost
	require.ErrorIs(t, resume.Handle(t.Context(), request), lost)

	require.NoError(t, resume.Handle(t.Context(), request))
	require.Len(t, computer.asked, 1)
	require.Len(t, sessions.admittedStarts(), 1)
}

// A request answered with its Work Key's existing Work returns that Work on
// every repeat, and never asks its Computer.
func TestAReusedWorkAnswersEveryRepeatOfItsRequest(t *testing.T) {
	computer := &deskLedger{}
	_, handler, request := creationFixture(t, computer)
	request.WorkCaller, request.WorkKey = domain.WorkCaller{Kind: "project", ID: "project-a"}, domain.WorkKey{Kind: "case", ID: "case-1"}
	id, err := handler.Handle(t.Context(), request)
	require.NoError(t, err)

	reuse := request
	initial := *request.InitialExecution
	initial.RequestID, initial.Input.ID = uuid.NewString(), uuid.NewString()
	reuse.RequestID, reuse.InitialExecution = initial.RequestID, &initial
	reused, err := handler.Handle(t.Context(), reuse)
	require.NoError(t, err)
	require.Equal(t, id, reused)
	repeated, err := handler.Handle(t.Context(), reuse)
	require.NoError(t, err)
	require.Equal(t, id, repeated)
	require.Len(t, computer.asked, 1, "reusing a Work asks its Computer nothing")
}

// A repeated Resume after a lost answer returns its original admission
// without a second start or Desk, whatever else it carries.
func TestARepeatedResumeReturnsItsOriginalOutcome(t *testing.T) {
	agents, sessions := startupStores(t)
	stopWork(t, sessions)
	computer := &deskLedger{}
	resume := command.NewResumeWorkHandler(time.Now, sessions, agents, sessionAccess{}, computer, querytest.ProvidedModels(), commandtest.UnchangedPlugins{})
	request := command.StartWork{RequestID: uuid.NewString(), SubjectID: "owner", WorkID: sessions.session.ID}
	require.NoError(t, resume.Handle(t.Context(), request))
	require.Equal(t, domain.WorkStarting, sessions.session.State)

	require.NoError(t, resume.Handle(t.Context(), request), "a repeat returns the original admission")
	changed := request
	changed.ThinkingLevel = "high"
	require.NoError(t, resume.Handle(t.Context(), changed))
	require.Len(t, computer.asked, 1, "a repeat whose Desk the current start holds asks its Computer nothing")
	require.Len(t, sessions.admittedStarts(), 1, "one start reaches execution")
}

// stopWork runs the Work's start to readiness, stops it and releases its
// execution, leaving it Stopped.
func stopWork(t *testing.T, sessions *feedbackSessionStore) {
	t.Helper()
	w := worldOver(t, sessions)
	w.ready(t)
	w.stop(t, "stop")
	w.exit(t, 0)
	w.release(t)
	require.Equal(t, domain.WorkStopped, sessions.session.State)
	sessions.committed = nil
}
