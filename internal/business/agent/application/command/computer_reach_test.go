package command_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// presencePlace is Computer's description of the Work's Computer.
type presencePlace struct {
	command.ComputerPort
	description command.ComputerDescription
	err         error
	reads       int
}

func (p *presencePlace) DescribeComputer(context.Context, string) (command.ComputerDescription, error) {
	p.reads++
	return p.description, p.err
}

// testClock is a clock the test moves.
type testClock struct{ at time.Time }

func (c *testClock) now() time.Time { return c.at }

// computerWorks lists the one Work as running on every Computer.
type computerWorks struct{ id string }

func (c computerWorks) ListRunningWorkIDs(_ context.Context, _, after string, _ int) ([]string, error) {
	if after != "" {
		return nil, nil
	}
	return []string{c.id}, nil
}

func (c computerWorks) ListHeldWorkIDs(ctx context.Context, computerID, after string, limit int) ([]string, error) {
	return c.ListRunningWorkIDs(ctx, computerID, after, limit)
}

// managedWorking is a Working Work on a Managed Computer, with its running
// execution.
func managedWorking(t *testing.T) *world {
	t.Helper()
	_, w := runningMessagingWorld(t, "work")
	w.work().PermissionMode = domain.PermissionBypass
	require.Equal(t, domain.WorkWorking, w.work().State)
	return w
}

// reachOver is the Computer reach of the world's Work, its Computer described
// by place.
func reachOver(w *world, now func() time.Time, place command.ComputerPort) *command.ComputerReachHandler {
	return command.NewComputerReachHandler(now, w.works, place, computerWorks{id: w.work().ID}, w.executions, w.coordination, w.harness, nil)
}

// recoverOver is the Work's recovery over its Computer reach.
func recoverOver(w *world, now func() time.Time, reach *command.ComputerReachHandler) *command.RecoverWorkHandler {
	return command.NewRecoverWorkHandler(now, w.works, w.harness, command.NewInputAcceptance(w.works, w.harness), w.settlements, reach)
}

// A Presence change records and ends the Disconnection of each Work running
// on the Computer, from the Presence Computer holds now, without changing
// its State.
func TestPresenceChangesRecordAndEndTheDisconnectionOfRunningWorks(t *testing.T) {
	ctx := context.Background()
	w := managedWorking(t)
	sessions := w.works
	lost := time.Now()
	place := &presencePlace{description: command.ComputerDescription{Managed: true, UnreachableSince: lost}}
	reach := reachOver(w, time.Now, place)

	require.NoError(t, reach.PresenceChanged(ctx, sessions.session.ComputerID))
	require.Equal(t, domain.WorkWorking, sessions.session.State)
	require.Equal(t, lost.UTC(), sessions.session.DisconnectedSince)
	saves := sessions.saves
	require.NoError(t, reach.PresenceChanged(ctx, sessions.session.ComputerID))
	require.Equal(t, saves, sessions.saves, "a repeated fact changes nothing")

	place.description = command.ComputerDescription{Managed: true, Reachable: true}
	require.NoError(t, reach.PresenceChanged(ctx, sessions.session.ComputerID))
	require.False(t, sessions.session.Disconnected())
	require.Equal(t, domain.WorkWorking, sessions.session.State)
	require.Equal(t, []domain.EventKind{domain.EventKindWorkStartAdmitted}, intents(sessions.session), "the run holds no exit or release request")
	require.False(t, w.execution(t).Retired())

	place.description = command.ComputerDescription{Managed: false}
	saves = sessions.saves
	require.NoError(t, reach.PresenceChanged(ctx, sessions.session.ComputerID))
	require.Equal(t, saves, sessions.saves, "a Cloud's Presence says nothing about its Works")
}

// Recovery reads the Work's Computer's Presence: past the Disconnection
// timeout it stops the Work beyond reach, retiring its run without
// reclaiming compute, and an observed Computer ends a Disconnection whose
// Reachable fact was lost.
func TestRecoveryStopsAManagedWorkBeyondReach(t *testing.T) {
	ctx := context.Background()
	w := managedWorking(t)
	sessions := w.works
	_, err := sessions.session.RecordDisconnection(time.Now())
	require.NoError(t, err)
	place := &presencePlace{description: command.ComputerDescription{Managed: true, Reachable: true}}
	clock := &testClock{at: time.Now().Add(domain.DisconnectionTimeout + time.Minute)}
	reach := reachOver(w, clock.now, place)
	recover := recoverOver(w, clock.now, reach)

	require.NoError(t, recover.Handle(ctx, sessions.session.ID))
	require.False(t, sessions.session.Disconnected(), "a reachable Computer ends the Disconnection")
	require.Equal(t, domain.WorkWorking, sessions.session.State)

	place.description = command.ComputerDescription{Managed: true, UnreachableSince: time.Now()}
	clock.at = clock.at.Add(time.Minute)
	require.NoError(t, recover.Handle(ctx, sessions.session.ID))
	require.Equal(t, domain.WorkStopped, sessions.session.State)
	require.Equal(t, domain.StopReasonDisconnected, sessions.session.StopReason)
	require.NoError(t, w.coordination.RetireExecution(ctx, sessions.session.ID))
	require.Equal(t, domain.RetiredFenced, w.execution(t).Retirement, "the fenced run is retired as fenced, not by an exit")
	require.Empty(t, w.environment.Deleted, "nothing is deleted on the machine while it is away")
}

// A loss newer than the Disconnection a Work records follows a return of
// reach the Work never saw, lost or not yet handled: the machine fences from
// the newer loss, so the Work is stopped only once that one has lasted the
// Disconnection timeout.
func TestANewerLossAfterAnUnseenReturnRestartsTheDisconnection(t *testing.T) {
	ctx := context.Background()
	w := managedWorking(t)
	sessions := w.works
	first := time.Now()
	place := &presencePlace{description: command.ComputerDescription{Managed: true, UnreachableSince: first}}
	clock := &testClock{at: first}
	reach := reachOver(w, clock.now, place)
	require.NoError(t, reach.PresenceChanged(ctx, sessions.session.ComputerID))
	require.Equal(t, first.UTC(), sessions.session.DisconnectedSince)

	// The machine came back and dropped again; only the second loss is seen.
	again := first.Add(4 * time.Minute)
	place.description = command.ComputerDescription{Managed: true, UnreachableSince: again}
	clock.at = first.Add(domain.DisconnectionTimeout + time.Second)
	require.NoError(t, reach.PresenceChanged(ctx, sessions.session.ComputerID))
	require.Equal(t, again.UTC(), sessions.session.DisconnectedSince)
	require.Equal(t, domain.WorkWorking, sessions.session.State, "the machine has not fenced the newer loss yet")

	clock.at = again.Add(domain.DisconnectionTimeout)
	require.NoError(t, reach.PresenceChanged(ctx, sessions.session.ComputerID))
	require.Equal(t, domain.WorkStopped, sessions.session.State)
}

// The recoveries of a sweep share one read of a Computer's Presence; a
// Presence change reads it fresh.
func TestRecoveriesShareARecentPresenceRead(t *testing.T) {
	ctx := context.Background()
	w := managedWorking(t)
	sessions := w.works
	place := &presencePlace{description: command.ComputerDescription{Managed: true, Reachable: true}}
	clock := &testClock{at: time.Now()}
	reach := reachOver(w, clock.now, place)
	recover := recoverOver(w, clock.now, reach)

	require.NoError(t, recover.Handle(ctx, sessions.session.ID))
	require.NoError(t, recover.Handle(ctx, sessions.session.ID))
	require.Equal(t, 1, place.reads)
	require.NoError(t, reach.PresenceChanged(ctx, sessions.session.ComputerID))
	require.Equal(t, 2, place.reads, "a Presence change is read fresh")
	clock.at = clock.at.Add(time.Minute)
	require.NoError(t, recover.Handle(ctx, sessions.session.ID))
	require.Equal(t, 3, place.reads, "an old read is not reused")
}

// A Presence that cannot be read holds back none of the Work's other
// recovery: a due timeout stop still applies, and the failure is returned.
func TestAnUnreadablePresenceHoldsBackNoOtherRecovery(t *testing.T) {
	ctx := context.Background()
	w := managedWorking(t)
	sessions := w.works
	origin := time.Now().Add(-domain.WaitingTimeout - time.Minute)
	require.NoError(t, sessions.session.ObserveTurn(domain.TurnObservation{Sequence: 1, Active: true}, false, false, origin))
	require.NoError(t, sessions.session.ApplyResponseRequirement(1000, true, origin))
	unavailable := errors.New("computer unavailable")
	place := &presencePlace{err: unavailable}
	reach := reachOver(w, time.Now, place)
	recover := recoverOver(w, time.Now, reach)

	require.ErrorIs(t, recover.Handle(ctx, sessions.session.ID), unavailable)
	require.Equal(t, domain.WorkStopping, sessions.session.State, "the waiting timeout still stops the Work")
	stops := sessions.committedStops()
	require.Len(t, stops, 1)
	require.True(t, stops[0].CancelWaitingTurn, "the held Turn is cancelled before the exit signal")
}
