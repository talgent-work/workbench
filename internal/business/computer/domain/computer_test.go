package domain_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/computer/domain"
)

var now = time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)

func cloud(t *testing.T, capacity domain.Room, policy domain.AdmissionPolicy) *domain.Computer {
	t.Helper()
	computer, err := domain.NewCloud("01a19bc3-075a-7c57-b42b-7fdf73b611ed", "Cloud", capacity, policy)
	require.NoError(t, err)
	return computer
}

func run(demand domain.Room) domain.RunAdmission {
	return domain.RunAdmission{DeskID: uuid.NewString(), WorkID: uuid.NewString(), WorkOwnerID: "owner", StarterID: "owner", Demand: demand, QuotaFits: true, Now: now}
}

var oneRun = domain.Room{MilliCPU: 600, MemoryBytes: 1 << 30, Desks: 1}

func TestCloudSeatsRunsUntilFullThenRejects(t *testing.T) {
	computer := cloud(t, domain.Room{MilliCPU: 1200, MemoryBytes: 2 << 30, Desks: 10}, domain.AdmissionReject)
	for range 2 {
		desk, rejection, err := computer.AdmitRun(run(oneRun))
		require.NoError(t, err)
		require.Empty(t, rejection)
		require.Equal(t, domain.DeskStateOccupied, desk.State)
	}
	require.Equal(t, domain.Room{MilliCPU: 1200, MemoryBytes: 2 << 30, Desks: 2}, computer.Occupied)
	desk, rejection, err := computer.AdmitRun(run(oneRun))
	require.NoError(t, err)
	require.Nil(t, desk)
	require.Equal(t, domain.RejectedFull, rejection)
}

func TestCloudRejectsWhatItsClusterQuotaCannotFit(t *testing.T) {
	computer := cloud(t, domain.Room{MilliCPU: 6000, MemoryBytes: 8 << 30, Desks: 10}, domain.AdmissionReject)
	admission := run(oneRun)
	admission.QuotaFits = false
	desk, rejection, err := computer.AdmitRun(admission)
	require.NoError(t, err)
	require.Nil(t, desk)
	require.Equal(t, domain.RejectedFull, rejection)
	require.Equal(t, domain.Room{}, computer.Occupied)
}

func TestFullQueueingComputerSeatsTheHeadWhenADeskLeaves(t *testing.T) {
	computer := cloud(t, domain.Room{MilliCPU: 600, MemoryBytes: 1 << 30, Desks: 1}, domain.AdmissionQueue)
	seated, _, err := computer.AdmitRun(run(oneRun))
	require.NoError(t, err)
	first, rejection, err := computer.AdmitRun(run(oneRun))
	require.NoError(t, err)
	require.Empty(t, rejection)
	require.Equal(t, domain.DeskStateWaiting, first.State)
	second, _, err := computer.AdmitRun(run(oneRun))
	require.NoError(t, err)
	require.Len(t, computer.WaitingLine, 2)

	seated.Leave()
	require.NoError(t, computer.FreeDesk(lastVacated(t, seated)))
	require.Equal(t, []domain.WaitingDesk{{DeskID: second.ID, Demand: oneRun}}, computer.WaitingLine)
	require.Equal(t, oneRun, computer.Occupied)
	assigned := computer.Events.Drain()
	require.Len(t, assigned, 1)
	require.Equal(t, domain.DeskAssigned{ComputerID: computer.ID, DeskID: first.ID}, assigned[0])

	// A withdrawn waiting Desk gives up its place without freeing room.
	second.Leave()
	require.NoError(t, computer.FreeDesk(lastVacated(t, second)))
	require.Empty(t, computer.WaitingLine)
	require.Equal(t, oneRun, computer.Occupied)
}

func TestDeskLeavesOnce(t *testing.T) {
	computer := cloud(t, domain.Room{MilliCPU: 1200, MemoryBytes: 2 << 30, Desks: 2}, domain.AdmissionReject)
	desk, _, err := computer.AdmitRun(run(oneRun))
	require.NoError(t, err)
	desk.Leave()
	desk.Leave()
	vacated := 0
	for _, event := range desk.Events.Drain() {
		if _, ok := event.(domain.DeskVacated); ok {
			vacated++
		}
	}
	require.Equal(t, 1, vacated)
	require.Error(t, desk.TakeSeat(now))
}

func TestCapacityNeverDropsBelowOccupied(t *testing.T) {
	computer := cloud(t, domain.Room{MilliCPU: 1200, MemoryBytes: 2 << 30, Desks: 2}, domain.AdmissionReject)
	_, _, err := computer.AdmitRun(run(oneRun))
	require.NoError(t, err)
	require.ErrorIs(t, computer.SetRoomRules(domain.Room{MilliCPU: 100, MemoryBytes: 2 << 30, Desks: 2}, domain.AdmissionReject), domain.ErrCapacityBelowOccupied)
	require.NoError(t, computer.SetRoomRules(domain.Room{MilliCPU: 600, MemoryBytes: 1 << 30, Desks: 1}, domain.AdmissionQueue))
}

func TestManagedComputerServesOnlyItsOwnerAndRefusesWhenAway(t *testing.T) {
	computer := &domain.Computer{ID: uuid.NewString(), Kind: domain.KindManaged, OwnerID: "owner", Name: "Laptop", Capacity: domain.Room{Desks: 2}, Policy: domain.AdmissionReject, Presence: domain.Presence{Reachable: true}, Versions: domain.ReportedVersions{TalgentComputer: "1.4.0"}, State: domain.StateActive}
	require.NoError(t, computer.Validate())
	desk := domain.Room{Desks: 1}

	other := run(desk)
	other.StarterID = "someone-else"
	_, rejection, err := computer.AdmitRun(other)
	require.NoError(t, err)
	require.Equal(t, domain.RejectedNotUsable, rejection)

	outdated := run(desk)
	outdated.MinimumVersion = "1.5.0"
	_, rejection, err = computer.AdmitRun(outdated)
	require.NoError(t, err)
	require.Equal(t, domain.RejectedOutdated, rejection)

	computer.Presence = domain.Presence{UnreachableSince: now}
	_, rejection, err = computer.AdmitRun(run(desk))
	require.NoError(t, err)
	require.Equal(t, domain.RejectedUnreachable, rejection)
}

func lastVacated(t *testing.T, desk *domain.Desk) domain.DeskVacated {
	t.Helper()
	events := desk.Events.Drain()
	vacated, ok := events[len(events)-1].(domain.DeskVacated)
	require.True(t, ok)
	return vacated
}

func TestComputerRefusesToFreeRoomItDoesNotHold(t *testing.T) {
	computer := cloud(t, domain.Room{MilliCPU: 1200, MemoryBytes: 2 << 30, Desks: 2}, domain.AdmissionReject)
	err := computer.FreeDesk(domain.DeskVacated{DeskID: "desk", ComputerID: computer.ID, Demand: oneRun, Seated: true})
	require.ErrorIs(t, err, domain.ErrInvalidComputer)
	require.Equal(t, domain.Room{}, computer.Occupied)
}
