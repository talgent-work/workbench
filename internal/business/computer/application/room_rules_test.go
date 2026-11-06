package application_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/computer/application"
	"github.com/talgent-work/workbench/internal/business/computer/domain"
)

type recordedSeatings struct {
	mu     sync.Mutex
	seated []application.SeatedDesk
}

func (r *recordedSeatings) Publish(_ context.Context, seated []application.SeatedDesk) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seated = append(r.seated, seated...)
}

// managedReads reads a Managed Computer from the repository fake.
type managedReads struct {
	application.Queries
	m *memory
}

func (q managedReads) GetManagedComputer(_ context.Context, owner, id string) (application.ManagedComputer, error) {
	c, ok := q.m.computers[id]
	if !ok || c.OwnerID != owner {
		return application.ManagedComputer{}, domain.ErrComputerNotFound
	}
	return application.ManagedComputer{ID: c.ID, Desks: c.Capacity.Desks, Occupied: c.Occupied.Desks, Waiting: len(c.WaitingLine), Policy: c.Policy}, nil
}

func managedApp(t *testing.T) (*application.Application, *memory, *recordedSeatings, string) {
	t.Helper()
	m := &memory{computers: map[string]domain.Computer{}, desks: map[string]domain.Desk{}}
	now := time.Now()
	computer, err := domain.Register(domain.Registration{ComputerID: uuid.NewString(), OwnerID: "owner", Name: "laptop", Now: now,
		Credential: domain.Credential{Digest: domain.DigestCredential("secret"), ExpiresAt: now.Add(time.Hour)}})
	require.NoError(t, err)
	require.NoError(t, computer.RegainReach())
	require.NoError(t, computer.ReportVersions(domain.ReportedVersions{TalgentComputer: "1.0.0"}))
	m.computers[computer.ID] = *computer
	seatings := &recordedSeatings{}
	app := application.NewApplication(application.Dependencies{Computers: m, Desks: desks{m}, Transactions: inline{}, Quota: quota(true), Queries: managedReads{m: m},
		Cloud: application.Cloud{ID: cloudID, Name: "Cloud", Capacity: oneRun, RunDemand: oneRun}, Seatings: seatings, Now: time.Now})
	return app, m, seatings, computer.ID
}

func admitOn(t *testing.T, app *application.Application, computerID string) (string, string, application.Admission) {
	t.Helper()
	desk, work := uuid.NewString(), uuid.NewString()
	admission, err := app.AdmitRun(context.Background(), application.AdmitRun{ComputerID: computerID, DeskID: desk, WorkID: work, WorkOwnerID: "owner", StarterID: "owner"})
	require.NoError(t, err)
	return desk, work, admission
}

// A full queueing Managed Computer queues a run, seats it once room frees
// up, and publishes its seating after the commit.
func TestAQueuedRunIsSeatedWhenRoomFreesUp(t *testing.T) {
	ctx := context.Background()
	app, m, seatings, computerID := managedApp(t)
	_, err := app.SetRoomRules(ctx, application.SetRoomRules{OwnerID: "someone else", ComputerID: computerID, Desks: 1, Policy: domain.AdmissionQueue})
	require.ErrorIs(t, err, domain.ErrComputerNotFound, "only its owner sets its rules")
	rules, err := app.SetRoomRules(ctx, application.SetRoomRules{OwnerID: "owner", ComputerID: computerID, Desks: 1, Policy: domain.AdmissionQueue})
	require.NoError(t, err)
	require.Equal(t, domain.AdmissionQueue, rules.Policy)

	running, _, seated := admitOn(t, app, computerID)
	require.Equal(t, application.AnswerSeated, seated.Answer)
	first, firstWork, queued := admitOn(t, app, computerID)
	require.Equal(t, application.AnswerWaiting, queued.Answer)
	second, secondWork, _ := admitOn(t, app, computerID)
	standing, err := app.Desk(ctx, first)
	require.NoError(t, err)
	require.Equal(t, application.DeskStanding{State: domain.DeskStateWaiting}, standing, "a waiting Desk has no seating time")

	require.NoError(t, app.LeaveDesk(ctx, running))
	standing, err = app.Desk(ctx, first)
	require.NoError(t, err)
	require.Equal(t, application.DeskStanding{State: domain.DeskStateOccupied, SeatedAt: m.desks[first].Since}, standing, "the head of the line is seated with the room the Desk freed")
	require.Equal(t, []application.SeatedDesk{{ComputerID: computerID, DeskID: first, WorkID: firstWork, SeatedAt: m.desks[first].Since}}, seatings.seated)

	// Room its owner adds seats the next waiting run at once.
	rules, err = app.SetRoomRules(ctx, application.SetRoomRules{OwnerID: "owner", ComputerID: computerID, Desks: 2, Policy: domain.AdmissionReject})
	require.NoError(t, err)
	require.EqualValues(t, 2, rules.Occupied)
	require.Zero(t, rules.Waiting)
	require.Len(t, seatings.seated, 2)
	require.Equal(t, secondWork, seatings.seated[1].WorkID)
	require.Equal(t, second, seatings.seated[1].DeskID)
	_, _, rejected := admitOn(t, app, computerID)
	require.Equal(t, application.AnswerRejected, rejected.Answer)
	require.Equal(t, domain.RejectedFull, rejected.Rejection)
	_, err = app.SetRoomRules(ctx, application.SetRoomRules{OwnerID: "owner", ComputerID: computerID, Desks: 1, Policy: domain.AdmissionReject})
	require.ErrorIs(t, err, domain.ErrCapacityBelowOccupied)

	_, err = app.Desk(ctx, uuid.NewString())
	require.ErrorIs(t, err, domain.ErrDeskNotFound)
}
