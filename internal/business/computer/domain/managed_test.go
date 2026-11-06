package domain

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func registered(t *testing.T, now time.Time) *Computer {
	t.Helper()
	computer, err := Register(Registration{ComputerID: uuid.NewString(), OwnerID: "person", Name: " laptop ", Now: now,
		Credential: Credential{Digest: DigestCredential("secret"), ExpiresAt: now.Add(time.Hour)}})
	require.NoError(t, err)
	return computer
}

func TestARegisteredComputerBelongsToItsApproverAndIsNotYetReachable(t *testing.T) {
	now := time.Now()
	computer := registered(t, now)
	require.Equal(t, KindManaged, computer.Kind)
	require.Equal(t, "laptop", computer.Name)
	require.Equal(t, Room{Desks: ManagedDesks}, computer.Capacity)
	require.Equal(t, Presence{UnreachableSince: now}, computer.Presence)
	require.True(t, computer.UsableBy("person", "person"))
	require.False(t, computer.UsableBy("person", "other"))

	_, err := Register(Registration{ComputerID: uuid.NewString(), OwnerID: "person", Name: "x", Now: now, Credential: Credential{Digest: "short", ExpiresAt: now.Add(time.Hour)}})
	require.ErrorIs(t, err, ErrInvalidComputer)
	_, err = Register(Registration{ComputerID: uuid.NewString(), OwnerID: "", Name: "x", Now: now, Credential: Credential{Digest: DigestCredential("s"), ExpiresAt: now.Add(time.Hour)}})
	require.ErrorIs(t, err, ErrInvalidComputer, "a Managed Computer has an owner")
}

func TestTheCredentialIsAcceptedOnlyWhileCurrent(t *testing.T) {
	now := time.Now()
	computer := registered(t, now)
	require.NoError(t, computer.Authenticate("secret", now))
	require.ErrorIs(t, computer.Authenticate("other", now), ErrCredentialRejected)
	require.ErrorIs(t, computer.Authenticate("secret", now.Add(time.Hour)), ErrCredentialRejected)
	computer.State = StateRemoved
	require.ErrorIs(t, computer.Authenticate("secret", now), ErrComputerRemoved)
}

func TestReachIsRecordedOncePerChange(t *testing.T) {
	now := time.Now()
	computer := registered(t, now)
	require.NoError(t, computer.ReportVersions(ReportedVersions{TalgentComputer: " 0.1.0 "}))
	require.Equal(t, "0.1.0", computer.Versions.TalgentComputer)
	require.NoError(t, computer.RegainReach())
	require.NoError(t, computer.RegainReach())
	lost := now.Add(time.Minute)
	require.NoError(t, computer.LoseReach(lost))
	require.NoError(t, computer.LoseReach(lost.Add(time.Minute)), "the first loss time stays")
	require.Equal(t, Presence{UnreachableSince: lost}, computer.Presence)
	events := computer.Events.Drain()
	require.Len(t, events, 2)
	require.Equal(t, EventKindComputerBecameReachable, events[0].Kind())
	require.Equal(t, EventKindComputerBecameUnreachable, events[1].Kind())
}

func TestRenameKeepsANameWithinItsBounds(t *testing.T) {
	computer := registered(t, time.Now())
	require.NoError(t, computer.Rename(" Studio "))
	require.Equal(t, "Studio", computer.Name)
	require.ErrorIs(t, computer.Rename(strings.Repeat("x", MaxNameLength+1)), ErrInvalidComputer)
	cloud, err := NewCloud(uuid.NewString(), "Cloud", Room{Desks: 1}, AdmissionReject)
	require.NoError(t, err)
	require.ErrorIs(t, cloud.Rename("mine"), ErrInvalidComputer)
	require.ErrorIs(t, cloud.LoseReach(time.Now()), ErrInvalidComputer)
}

func managedRun(computer *Computer) RunAdmission {
	return RunAdmission{DeskID: uuid.NewString(), WorkID: uuid.NewString(), WorkOwnerID: computer.OwnerID, StarterID: computer.OwnerID, Demand: computer.RunDemand(Room{}), Now: time.Now()}
}

func TestItsOwnerSetsAManagedComputersDesksAndPolicy(t *testing.T) {
	computer := registered(t, time.Now())
	require.NoError(t, computer.RegainReach())
	require.NoError(t, computer.ReportVersions(ReportedVersions{TalgentComputer: "1.0.0"}))
	computer.Events.Drain()
	for _, capacity := range []Room{{}, {Desks: MaxManagedDesks + 1}, {Desks: 2, MilliCPU: 100}} {
		require.ErrorIs(t, computer.SetRoomRules(capacity, AdmissionQueue), ErrInvalidComputer, "a Managed Computer runs 1 to %d Works", MaxManagedDesks)
	}
	require.ErrorIs(t, computer.SetRoomRules(Room{Desks: 2}, "later"), ErrInvalidComputer)

	require.NoError(t, computer.SetRoomRules(Room{Desks: 1}, AdmissionQueue))
	seated, rejection, err := computer.AdmitRun(managedRun(computer))
	require.NoError(t, err)
	require.Empty(t, rejection)
	require.Equal(t, DeskStateOccupied, seated.State)
	waiting, _, err := computer.AdmitRun(managedRun(computer))
	require.NoError(t, err)
	require.Equal(t, DeskStateWaiting, waiting.State, "a full queueing Computer queues the run")

	// Room its owner adds seats the head of the line at once.
	require.NoError(t, computer.SetRoomRules(Room{Desks: 2}, AdmissionReject))
	require.Empty(t, computer.WaitingLine)
	require.Equal(t, Room{Desks: 2}, computer.Occupied)
	require.Equal(t, []Event{DeskAssigned{ComputerID: computer.ID, DeskID: waiting.ID}}, computer.Events.Drain())
	_, rejection, err = computer.AdmitRun(managedRun(computer))
	require.NoError(t, err)
	require.Equal(t, RejectedFull, rejection, "a full rejecting Computer rejects the run")
	require.ErrorIs(t, computer.SetRoomRules(Room{Desks: 1}, AdmissionReject), ErrCapacityBelowOccupied)

	computer.State = StateRemoved
	require.ErrorIs(t, computer.SetRoomRules(Room{Desks: 3}, AdmissionQueue), ErrComputerRemoved)
}

// A run asking for Claude Code's sandbox is admitted only on a machine that
// reports it can provide it, as its latest connection said.
func TestASandboxedRunNeedsAMachineThatProvidesTheSandbox(t *testing.T) {
	now := time.Now()
	computer := registered(t, now)
	require.NoError(t, computer.ReportVersions(ReportedVersions{TalgentComputer: "0.1.0"}))
	require.NoError(t, computer.RegainReach())
	run := RunAdmission{DeskID: uuid.NewString(), WorkID: uuid.NewString(), WorkOwnerID: "person", StarterID: "person", Demand: Room{Desks: 1}, Sandbox: true, Now: now}
	desk, rejection, err := computer.AdmitRun(run)
	require.NoError(t, err)
	require.Nil(t, desk)
	require.Equal(t, RejectedNoSandbox, rejection)

	require.NoError(t, computer.ReportSandbox(true))
	desk, rejection, err = computer.AdmitRun(run)
	require.NoError(t, err)
	require.Empty(t, rejection)
	require.NotNil(t, desk)

	cloud, err := NewCloud(uuid.NewString(), "Cloud", Room{Desks: 4}, AdmissionReject)
	require.NoError(t, err)
	require.ErrorIs(t, cloud.ReportSandbox(true), ErrInvalidComputer)
}
