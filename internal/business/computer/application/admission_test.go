package application_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/computer/application"
	"github.com/talgent-work/workbench/internal/business/computer/domain"
)

const cloudID = "01a19bc3-075a-7c57-b42b-7fdf73b611ed"

type memory struct {
	computers map[string]domain.Computer
	desks     map[string]domain.Desk
}

func (m *memory) Get(_ context.Context, id string) (*domain.Computer, error) {
	c, ok := m.computers[id]
	if !ok {
		return nil, domain.ErrComputerNotFound
	}
	c.Events = domain.NewEventCollection()
	return &c, nil
}

func (m *memory) Save(_ context.Context, c *domain.Computer) error {
	if stored, ok := m.computers[c.ID]; ok && stored.Version != c.Version {
		return domain.ErrConcurrentModification
	}
	saved := *c
	saved.Version++
	m.computers[c.ID] = saved
	return nil
}

func (m *memory) FindByCredential(ctx context.Context, digest string) (*domain.Computer, error) {
	for id, c := range m.computers {
		if digest != "" && (c.Credential.Digest == digest || c.PendingCredential.Digest == digest || c.RetiredCredential.Digest == digest) {
			return m.Get(ctx, id)
		}
	}
	return nil, domain.ErrComputerNotFound
}

type desks struct{ *memory }

func (d desks) Get(_ context.Context, id string) (*domain.Desk, error) {
	desk, ok := d.desks[id]
	if !ok {
		return nil, domain.ErrDeskNotFound
	}
	desk.Events = domain.NewEventCollection()
	return &desk, nil
}

func (d desks) Save(_ context.Context, desk *domain.Desk) error {
	saved := *desk
	saved.Version++
	d.desks[desk.ID] = saved
	return nil
}

func (d desks) ListOpen(_ context.Context, computerID string) ([]*domain.Desk, error) {
	var open []*domain.Desk
	for id, desk := range d.desks {
		if desk.ComputerID == computerID && desk.State != domain.DeskStateVacated {
			found, _ := d.Get(context.Background(), id)
			open = append(open, found)
		}
	}
	return open, nil
}

type inline struct{}

func (inline) Within(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

type quota bool

func (q quota) FitsRun(context.Context) (bool, error) { return bool(q), nil }

var oneRun = domain.Room{MilliCPU: 1000, MemoryBytes: 1 << 30, Desks: 1}

func newApp(t *testing.T, capacity domain.Room) (*application.Application, *memory) {
	t.Helper()
	m := &memory{computers: map[string]domain.Computer{}, desks: map[string]domain.Desk{}}
	app := application.NewApplication(application.Dependencies{Computers: m, Desks: desks{m}, Transactions: inline{}, Quota: quota(true),
		Cloud: application.Cloud{ID: cloudID, Name: "Cloud", Capacity: capacity, RunDemand: oneRun}, Now: time.Now})
	require.NoError(t, app.EnsureCloud(context.Background(), slog.Default()))
	return app, m
}

func admit(t *testing.T, app *application.Application) (string, application.Admission) {
	t.Helper()
	desk := uuid.NewString()
	admission, err := app.AdmitRun(context.Background(), application.AdmitRun{ComputerID: cloudID, DeskID: desk, WorkID: uuid.NewString(), WorkOwnerID: "owner", StarterID: "owner"})
	require.NoError(t, err)
	return desk, admission
}

// A lowered Cloud Capacity is refused while Desks occupy more, and taken up
// once enough of them leave.
func TestCloudTakesUpALowerCapacityOnceDesksLeave(t *testing.T) {
	two := domain.Room{MilliCPU: 2000, MemoryBytes: 2 << 30, Desks: 2}
	app, m := newApp(t, two)
	first, _ := admit(t, app)
	second, _ := admit(t, app)

	// The platform lowers the Cloud to one run while two are seated.
	lowered := application.NewApplication(application.Dependencies{Computers: m, Desks: desks{m}, Transactions: inline{}, Quota: quota(true),
		Cloud: application.Cloud{ID: cloudID, Name: "Cloud", Capacity: oneRun, RunDemand: oneRun}, Now: time.Now})
	require.NoError(t, lowered.EnsureCloud(context.Background(), slog.Default()))
	require.Equal(t, two, m.computers[cloudID].Capacity, "capacity never drops below occupied room")

	require.NoError(t, lowered.LeaveDesk(context.Background(), first))
	require.Equal(t, oneRun, m.computers[cloudID].Capacity, "the lower capacity applies once the room fits")
	_, admission := admit(t, lowered)
	require.Equal(t, application.AnswerRejected, admission.Answer)
	require.Equal(t, domain.RejectedFull, admission.Rejection)

	require.NoError(t, lowered.LeaveDesk(context.Background(), second))
	require.NoError(t, lowered.LeaveDesk(context.Background(), second), "leaving again does nothing")
	require.Equal(t, domain.Room{}, m.computers[cloudID].Occupied)
}

// Each answer names what the admitting Computer is, a repeated one too.
func TestRepeatedAdmissionOfADeskReturnsItsAnswer(t *testing.T) {
	app, m := newApp(t, oneRun)
	desk, admission := admit(t, app)
	require.Equal(t, application.Admission{Answer: application.AnswerSeated, Kind: domain.KindCloud}, admission)
	work := m.desks[desk].WorkID
	again, err := app.AdmitRun(context.Background(), application.AdmitRun{ComputerID: cloudID, DeskID: desk, WorkID: work, WorkOwnerID: "owner", StarterID: "owner"})
	require.NoError(t, err)
	require.Equal(t, application.Admission{Answer: application.AnswerSeated, Kind: domain.KindCloud}, again)
	require.Equal(t, oneRun, m.computers[cloudID].Occupied, "a repeated admission claims no more room")
	_, err = app.AdmitRun(context.Background(), application.AdmitRun{ComputerID: cloudID, DeskID: desk, WorkID: uuid.NewString()})
	require.ErrorIs(t, err, domain.ErrDeskTaken)
}

// A Managed Computer below the configured Minimum Version rejects a new run
// as outdated; one at it seats the run.
func TestAManagedComputerBelowTheMinimumVersionTakesNoRun(t *testing.T) {
	m := &memory{computers: map[string]domain.Computer{}, desks: map[string]domain.Desk{}}
	app := application.NewApplication(application.Dependencies{Computers: m, Desks: desks{m}, Transactions: inline{}, Quota: quota(true),
		Cloud: application.Cloud{ID: cloudID, Name: "Cloud", Capacity: oneRun, RunDemand: oneRun}, MinimumVersion: "0.4.0", Now: time.Now})
	require.Equal(t, "0.4.0", app.MinimumVersion(), "the machine is told the minimum admission uses")
	laptop := domain.Computer{ID: uuid.NewString(), Kind: domain.KindManaged, OwnerID: "owner", Name: "Laptop", Capacity: domain.Room{Desks: 2}, Policy: domain.AdmissionReject,
		Presence: domain.Presence{Reachable: true}, Versions: domain.ReportedVersions{TalgentComputer: "0.3.9"}, State: domain.StateActive}
	m.computers[laptop.ID] = laptop
	request := application.AdmitRun{ComputerID: laptop.ID, DeskID: uuid.NewString(), WorkID: uuid.NewString(), WorkOwnerID: "owner", StarterID: "owner"}

	admission, err := app.AdmitRun(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, application.Admission{Answer: application.AnswerRejected, Rejection: domain.RejectedOutdated, Kind: domain.KindManaged}, admission)
	require.Empty(t, m.desks, "a rejected run holds no Desk")

	upgraded := m.computers[laptop.ID]
	upgraded.Versions.TalgentComputer = "0.4.0"
	m.computers[laptop.ID] = upgraded
	admission, err = app.AdmitRun(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, application.Admission{Answer: application.AnswerSeated, Kind: domain.KindManaged}, admission)
}
