package domain_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// startOn is the Computer's answer seating a start at the named Desk.
func startOn(work *domain.AgentWork, deskID string) domain.StartAdmission {
	return domain.StartAdmission{AdmittedAt: time.Now(), AgentID: work.AgentID, DefinitionAdmitted: true, ModelConfigurationProvided: true,
		Seat: domain.ComputerSeat{ComputerID: work.ComputerID, Kind: domain.ComputerCloud, DeskID: deskID}}
}

func admittedStarts(work *domain.AgentWork) int {
	return requestsOf(work).starts
}

// Unique start request: a repeated Resume, whose Desk the current start
// holds, returns the same admission and starts nothing more.
func TestARepeatedResumeReturnsTheAdmissionItsDeskHolds(t *testing.T) {
	work, _ := stoppedWork(t)
	require.False(t, work.StartHoldsDesk("desk-resume"), "a stopped Work's ended start holds no Desk of this request")
	require.NoError(t, work.Resume(startOn(work, "desk-resume")))
	admitted := *work.HarnessProcess
	starts := admittedStarts(work)

	require.True(t, work.StartHoldsDesk("desk-resume"))
	require.NoError(t, work.Resume(startOn(work, "desk-resume")), "the repeat answers the same admission")
	require.Equal(t, admitted, *work.HarnessProcess, "the current start is unchanged")
	require.Equal(t, starts, admittedStarts(work), "no second start is admitted")

	ready(t, work)
	require.True(t, work.StartHoldsDesk("desk-resume"), "a running start still holds its Desk")
	require.NoError(t, work.Resume(startOn(work, "desk-resume")))
	require.Equal(t, domain.WorkWorking, work.State)
}

// A request deriving another Desk is a new Resume: it is admitted only when
// the Work may start now.
func TestAResumeOfAnotherDeskIsANewStart(t *testing.T) {
	work, _ := stoppedWork(t)
	require.NoError(t, work.Resume(startOn(work, "desk-resume")))
	require.False(t, work.StartHoldsDesk("desk-other"))
	require.ErrorIs(t, work.Resume(startOn(work, "desk-other")), domain.ErrWorkTransition)
}

// Once the start's compute is reclaimed its Desk has left: a late repeat is
// no longer recognized, and Computer refuses the Desk that left.
func TestAStartWhoseComputeIsReclaimedNoLongerHoldsItsDesk(t *testing.T) {
	work, _ := stoppedWork(t)
	require.NoError(t, work.Resume(startOn(work, "desk-resume")))
	ready(t, work)
	require.NoError(t, work.Stop("stop-2", false))
	exited(t, work, domain.HarnessExit{})
	require.True(t, work.StartHoldsDesk("desk-resume"), "until its compute is reclaimed the stopped start holds its Desk")
	released(t, work)
	require.False(t, work.StartHoldsDesk("desk-resume"))
	require.False(t, work.StartHoldsDesk(""), "no request derives an empty Desk")
}

// A repeated Restart is recognized the same way.
func TestARepeatedRestartReturnsTheAdmissionItsDeskHolds(t *testing.T) {
	work, _ := startingWork(t)
	rejected(t, work)
	released(t, work)
	require.NoError(t, work.Restart(startOn(work, "desk-restart")))
	admitted := *work.HarnessProcess
	starts := admittedStarts(work)

	require.NoError(t, work.Restart(startOn(work, "desk-restart")))
	require.Equal(t, admitted, *work.HarnessProcess)
	require.Equal(t, starts, admittedStarts(work))
	require.ErrorIs(t, work.Restart(startOn(work, "desk-other")), domain.ErrWorkTransition)
}

// Unique start request: a request whose Desk the Work's ended start took
// admits no second start, even before its Computer says the Desk left.
func TestARequestWhoseStartEndedAdmitsNoSecondStart(t *testing.T) {
	work, _ := stoppedWork(t)
	require.NoError(t, work.Resume(startOn(work, "desk-resume")))
	ready(t, work)
	require.NoError(t, work.Stop("stop-2", false))
	exited(t, work, domain.HarnessExit{})
	released(t, work)
	starts := admittedStarts(work)

	require.ErrorIs(t, work.Resume(startOn(work, "desk-resume")), domain.ErrStartRequestSpent)
	require.Equal(t, domain.WorkStopped, work.State)
	require.Equal(t, starts, admittedStarts(work))
	require.NoError(t, work.Resume(startOn(work, "desk-next")), "a new request starts the Work again")
}
