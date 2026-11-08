package command_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/application/query/querytest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// switchableComputer answers admission as unreachable until it is reached
// again, then by err.
type switchableComputer struct {
	capacityCheck
	unreachable bool
	admitted    int
}

func (c *switchableComputer) AdmitRun(ctx context.Context, run command.RunAdmission) (command.ComputerAnswer, error) {
	if c.unreachable {
		return command.ComputerAnswer{Kind: command.ComputerRejected, Rejection: domain.ErrComputerUnreachable}, nil
	}
	c.admitted++
	return c.capacityCheck.AdmitRun(ctx, run)
}

// markedWorks lists the one Work as marked on every Computer while it is.
type markedWorks struct{ sessions *feedbackSessionStore }

func (w markedWorks) ListReachResumeWorkIDs(_ context.Context, _, after string, _ int) ([]string, error) {
	if after != "" || !w.sessions.session.ReachResumeDue || w.sessions.session.State != domain.WorkStopped {
		return nil, nil
	}
	return []string{w.sessions.session.ID}, nil
}

type reconciliations struct {
	command.ExecutionEnvironmentPort
	computers []string
	err       error
}

func (r *reconciliations) ReconcileComputer(_ context.Context, computerID string) error {
	r.computers = append(r.computers, computerID)
	return r.err
}

// reconnectionFixture is a Stopped Work whose Resume its unreachable
// Managed Computer refused, with input buffered or not.
type reconnectionFixture struct {
	agents         *startupAgentStore
	sessions       *feedbackSessionStore
	computer       *switchableComputer
	buffer         *commandtest.InputBuffer
	resume         *command.ResumeWorkHandler
	reconciliation *reconciliations
	reconnection   *command.ComputerReconnectionHandler
}

func newReconnectionFixture(t *testing.T, buffered bool) *reconnectionFixture {
	t.Helper()
	agents, sessions := startupStores(t)
	sessions.session.State = domain.WorkStopped
	sessions.session.HarnessProcess.Exited, sessions.session.HarnessProcess.ComputeReclaimed = true, true
	f := &reconnectionFixture{agents: agents, sessions: sessions, computer: &switchableComputer{unreachable: true}, buffer: &commandtest.InputBuffer{}, reconciliation: &reconciliations{}}
	if buffered {
		_, err := f.buffer.BufferInput(context.Background(), sessions.session.ID, command.ExecutionInput{ID: "message-1", Text: "a message arrived while the machine was away"})
		require.NoError(t, err)
	}
	f.resume = command.NewResumeWorkHandler(time.Now, sessions, agents, sessionAccess{}, f.computer, querytest.ProvidedModels(), commandtest.UnchangedPlugins{})
	place := &presencePlace{description: command.ComputerDescription{Managed: true, Reachable: true}}
	var err error
	f.reconnection, err = command.NewComputerReconnectionHandler(place, sessions, agents, markedWorks{sessions: sessions}, f.buffer, f.resume, f.reconciliation, nil)
	require.NoError(t, err)
	// The person's message resumes the Work while its machine is away.
	err = f.resume.Handle(context.Background(), command.StartWork{SubjectID: "owner", WorkID: sessions.session.ID, RequestID: uuid.NewString()})
	require.ErrorIs(t, err, domain.ErrComputerUnreachable)
	require.Equal(t, domain.WorkStopped, sessions.session.State)
	require.True(t, sessions.session.ReachResumeDue, "a Resume refused as unreachable is marked")
	f.computer.unreachable = false
	return f
}

// A message that arrived while the machine was away triggers exactly one Resume
// once it reconnects, which continues the native session after the machine
// is reconciled; a repeated or concurrent reconnection resumes nothing more.
func TestAReconnectionRetriesTheRefusedResumeOnceWhenInputAwaits(t *testing.T) {
	ctx := context.Background()
	f := newReconnectionFixture(t, true)
	computerID := f.sessions.session.ComputerID
	require.NoError(t, f.reconnection.Reconnected(ctx, computerID))
	require.Equal(t, []string{computerID}, f.reconciliation.computers, "the machine is reconciled on its return")
	require.Equal(t, domain.WorkStarting, f.sessions.session.State)
	require.False(t, f.sessions.session.ReachResumeDue, "the admitted start clears the mark")
	require.Equal(t, domain.ContextContinue, f.sessions.session.HarnessProcess.ContextMode, "Resume continues the native session")
	require.Len(t, f.sessions.admittedStarts(), 1)
	// The start its execution launches continues the Work's native session.
	launch := f.sessions.session.LaunchSpecification()
	require.Equal(t, domain.ContextContinue, launch.ContextMode)
	require.Equal(t, f.sessions.session.HarnessSessionReference, launch.HarnessSessionReference)
	require.NotEmpty(t, launch.HarnessSessionReference)
	require.NoError(t, f.reconnection.Reconnected(ctx, computerID))
	require.Equal(t, 1, f.computer.admitted, "a repeated reconnection resumes nothing more")
	require.Len(t, f.sessions.admittedStarts(), 1)
}

// Without buffered input the reconnection resumes nothing and the mark is
// spent; a Work without the mark is never resumed by a reconnection.
func TestAReconnectionResumesNoWorkWithoutInputOrWithoutTheMark(t *testing.T) {
	ctx := context.Background()
	f := newReconnectionFixture(t, false)
	computerID := f.sessions.session.ComputerID
	require.NoError(t, f.reconnection.Reconnected(ctx, computerID))
	require.Equal(t, domain.WorkStopped, f.sessions.session.State)
	require.False(t, f.sessions.session.ReachResumeDue, "the reconnection that owed the Resume spent it")
	require.Zero(t, f.computer.admitted)

	unmarked := newReconnectionFixture(t, true)
	unmarked.sessions.session.ReachResumeDue = false
	require.NoError(t, unmarked.reconnection.Reconnected(ctx, computerID))
	require.Equal(t, domain.WorkStopped, unmarked.sessions.session.State)
	require.Zero(t, unmarked.computer.admitted)
	require.Empty(t, unmarked.sessions.admittedStarts())
}

// The one request consumes the mark whatever the Computer answers: refused
// for another reason, it is cleared; refused as unreachable again, it waits
// for the next reconnection.
func TestTheRetriedResumeIsRequestedOnceWhateverItsAnswer(t *testing.T) {
	ctx := context.Background()
	full := newReconnectionFixture(t, true)
	full.computer.err = domain.ErrComputerFull
	require.NoError(t, full.reconnection.Reconnected(ctx, full.sessions.session.ComputerID))
	require.Equal(t, domain.WorkStopped, full.sessions.session.State)
	require.False(t, full.sessions.session.ReachResumeDue, "a refusal for another reason spends the mark")

	away := newReconnectionFixture(t, true)
	away.computer.unreachable = true
	require.NoError(t, away.reconnection.Reconnected(ctx, away.sessions.session.ComputerID))
	require.True(t, away.sessions.session.ReachResumeDue, "a machine away again keeps the mark for its next return")
}

// A Computer that is not reachable now, a Cloud or a removed one, is not
// reconnected; a failed reconciliation is returned for the fact's retry
// without holding back the retried Resume, whose start reconciles itself.
func TestOnlyAReachableManagedComputerIsReconnected(t *testing.T) {
	ctx := context.Background()
	for name, description := range map[string]command.ComputerDescription{
		"unreachable": {Managed: true},
		"cloud":       {Reachable: true},
		"removed":     {Managed: true, Removed: true, Reachable: true},
	} {
		t.Run(name, func(t *testing.T) {
			f := newReconnectionFixture(t, true)
			reconnection, err := command.NewComputerReconnectionHandler(&presencePlace{description: description}, f.sessions, f.agents, markedWorks{sessions: f.sessions}, f.buffer, f.resume, f.reconciliation, nil)
			require.NoError(t, err)
			require.NoError(t, reconnection.Reconnected(ctx, f.sessions.session.ComputerID))
			require.Empty(t, f.reconciliation.computers)
			require.True(t, f.sessions.session.ReachResumeDue)
		})
	}
	f := newReconnectionFixture(t, true)
	f.reconciliation.err = errors.New("the machine did not answer")
	require.ErrorIs(t, f.reconnection.Reconnected(ctx, f.sessions.session.ComputerID), f.reconciliation.err)
	require.Equal(t, domain.WorkStarting, f.sessions.session.State)
}

// A retry that cannot be made now holds back neither the fact nor the other
// Works: it is logged, the mark stays, and the Work's recovery retries it,
// as it does after a lost Computer Became Reachable.
func TestTheWorksRecoveryRetriesAnOwedResumeTheReconnectionMissed(t *testing.T) {
	ctx := context.Background()
	f := newReconnectionFixture(t, true)
	f.buffer.Err = errors.New("the buffer is unavailable")
	require.NoError(t, f.reconnection.Reconnected(ctx, f.sessions.session.ComputerID), "a Work's failure does not retry the fact")
	require.True(t, f.sessions.session.ReachResumeDue)
	require.Equal(t, domain.WorkStopped, f.sessions.session.State)

	require.ErrorIs(t, f.reconnection.RetryOwedResume(ctx, f.sessions.session.ID), f.buffer.Err, "the recovery's retry reports its failure")
	f.buffer.Err = nil
	require.NoError(t, f.reconnection.RetryOwedResume(ctx, f.sessions.session.ID))
	require.Equal(t, domain.WorkStarting, f.sessions.session.State)
	require.Len(t, f.sessions.admittedStarts(), 1)
	require.NoError(t, f.reconnection.RetryOwedResume(ctx, f.sessions.session.ID))
	require.Len(t, f.sessions.admittedStarts(), 1, "an unmarked Work is not retried")

	away := newReconnectionFixture(t, true)
	reconnection, err := command.NewComputerReconnectionHandler(&presencePlace{description: command.ComputerDescription{Managed: true}}, away.sessions, away.agents, markedWorks{sessions: away.sessions}, away.buffer, away.resume, away.reconciliation, nil)
	require.NoError(t, err)
	require.NoError(t, reconnection.RetryOwedResume(ctx, away.sessions.session.ID))
	require.True(t, away.sessions.session.ReachResumeDue, "a machine still away keeps the mark")
	require.Zero(t, away.computer.admitted)
}

// A message that reaches a Working Work during the Disconnection grace waits in
// its input buffer; the stop beyond reach marks the Work, so the machine's
// return resumes it once and the message is read by the resumed Harness exactly
// once.
func TestAMailBufferedDuringTheGraceIsReadOnceAfterReconnection(t *testing.T) {
	ctx := context.Background()
	agents, w := runningMessagingWorld(t, "work")
	sessions := w.works
	sessions.session.PermissionMode = domain.PermissionBypass
	sessions.session.ComputerKind = domain.ComputerManaged
	buffer := w.harness.InputBuffer
	lost := time.Now()
	place := &presencePlace{description: command.ComputerDescription{Managed: true, UnreachableSince: lost}}
	clock := &testClock{at: lost}
	reach := command.NewComputerReachHandler(clock.now, sessions, place, computerWorks{id: sessions.session.ID}, w.executions, w.coordination, w.harness, nil)
	require.NoError(t, reach.PresenceChanged(ctx, sessions.session.ComputerID))
	require.True(t, sessions.session.Disconnected())
	_, err := buffer.BufferInput(ctx, sessions.session.ID, command.ExecutionInput{ID: "message-1", Text: "a message during the grace"})
	require.NoError(t, err)

	clock.at = lost.Add(domain.DisconnectionTimeout)
	require.NoError(t, reach.PresenceChanged(ctx, sessions.session.ComputerID))
	require.Equal(t, domain.WorkStopped, sessions.session.State)
	require.True(t, sessions.session.ReachResumeDue, "the message awaits the machine's return")

	starts := len(sessions.admittedStarts())
	computer := &switchableComputer{}
	resume := command.NewResumeWorkHandler(time.Now, sessions, agents, sessionAccess{}, computer, querytest.ProvidedModels(), commandtest.UnchangedPlugins{})
	place.description = command.ComputerDescription{Managed: true, Reachable: true}
	reconnection, err := command.NewComputerReconnectionHandler(place, sessions, agents, markedWorks{sessions: sessions}, buffer, resume, &reconciliations{}, nil)
	require.NoError(t, err)
	require.NoError(t, reconnection.Reconnected(ctx, sessions.session.ComputerID))
	require.NoError(t, reconnection.Reconnected(ctx, sessions.session.ComputerID))
	require.NoError(t, reconnection.RetryOwedResume(ctx, sessions.session.ID))
	require.Equal(t, domain.WorkStarting, sessions.session.State)
	require.Len(t, sessions.admittedStarts(), starts+1, "the machine's return resumes the Work once")
	require.Equal(t, domain.ContextContinue, sessions.session.LaunchSpecification().ContextMode)
	buffered, err := buffer.ReadBuffer(ctx, sessions.session.ID)
	require.NoError(t, err)
	require.Equal(t, []string{"message-1"}, buffered.InputIDs, "the message waits once for the resumed Harness")
}
