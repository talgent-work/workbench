package application_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/computer/application"
	"github.com/talgent-work/workbench/internal/business/computer/domain"
	"github.com/talgent-work/workbench/internal/business/computer/infrastructure/registration"
	"github.com/talgent-work/workbench/internal/pkg/computerconnection"
)

type managedFixture struct {
	app       *application.Application
	memory    *memory
	redis     *miniredis.Miniredis
	conns     *computerconnection.Writer
	presences *presences
	now       time.Time
}

// presences records the Presence changes published after their commit.
type presences struct{ published []application.PresenceChange }

func (p *presences) Publish(_ context.Context, changes []application.PresenceChange) {
	p.published = append(p.published, changes...)
}

func newManaged(t *testing.T) *managedFixture {
	t.Helper()
	server := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: server.Addr()})
	f := &managedFixture{memory: &memory{computers: map[string]domain.Computer{}, desks: map[string]domain.Desk{}}, redis: server, conns: computerconnection.NewWriter(rdb), presences: &presences{}, now: time.Now().UTC()}
	f.app = application.NewApplication(application.Dependencies{Computers: f.memory, Desks: desks{f.memory}, Transactions: inline{}, Quota: quota(true), Queries: queries{f.memory},
		Registrations: registration.NewStore(rdb), Connections: computerconnection.NewReader(rdb), Presences: f.presences, Now: func() time.Time { return f.now }})
	return f
}

type queries struct{ *memory }

func (q queries) ListUsableComputers(context.Context, string) ([]application.UsableComputer, error) {
	return nil, nil
}
func (q queries) ListOpenDesks(context.Context, time.Time, string, int) ([]application.OpenDesk, error) {
	return nil, nil
}
func (q queries) ListManagedComputers(_ context.Context, owner string) ([]application.ManagedComputer, error) {
	var list []application.ManagedComputer
	for _, c := range q.computers {
		if c.Kind == domain.KindManaged && c.OwnerID == owner {
			list = append(list, application.ManagedComputer{ID: c.ID, Name: c.Name, Presence: c.Presence, Versions: c.Versions})
		}
	}
	return list, nil
}
func (q queries) GetManagedComputer(ctx context.Context, owner, id string) (application.ManagedComputer, error) {
	list, _ := q.ListManagedComputers(ctx, owner)
	for _, c := range list {
		if c.ID == id {
			return c, nil
		}
	}
	return application.ManagedComputer{}, domain.ErrComputerNotFound
}
func (q queries) ListManagedPresence(_ context.Context, after string, _ int) ([]application.ManagedPresence, error) {
	var page []application.ManagedPresence
	for id, c := range q.computers {
		if c.Kind == domain.KindManaged && c.State == domain.StateActive && id > after {
			page = append(page, application.ManagedPresence{ID: id, Reachable: c.Presence.Reachable})
		}
	}
	slices.SortFunc(page, func(a, b application.ManagedPresence) int { return strings.Compare(a.ID, b.ID) })
	return page, nil
}

func (f *managedFixture) register(t *testing.T, owner string) application.Redemption {
	t.Helper()
	ctx := context.Background()
	started, err := f.app.StartRegistration(ctx, "  build-box  ")
	require.NoError(t, err)
	require.Len(t, started.UserCode, 8)
	pending, err := f.app.RedeemRegistration(ctx, started.DeviceCode)
	require.NoError(t, err)
	require.True(t, pending.Pending, "nothing is registered before approval")
	typed := started.UserCode[:4] + "-" + started.UserCode[4:]
	require.NoError(t, f.app.ApproveRegistration(ctx, typed, owner))
	require.NoError(t, f.app.ApproveRegistration(ctx, started.UserCode, owner), "approving again does nothing")
	require.ErrorIs(t, f.app.ApproveRegistration(ctx, started.UserCode, "someone-else"), application.ErrRegistrationTaken)
	redeemed, err := f.app.RedeemRegistration(ctx, started.DeviceCode)
	require.NoError(t, err)
	_, err = f.app.RedeemRegistration(ctx, started.DeviceCode)
	require.ErrorIs(t, err, application.ErrRegistrationNotFound, "the credential is issued once")
	return redeemed
}

func TestApprovedRegistrationRegistersAManagedComputerForItsApprover(t *testing.T) {
	f := newManaged(t)
	redeemed := f.register(t, "person")
	computer := f.memory.computers[redeemed.ComputerID]
	require.Equal(t, domain.KindManaged, computer.Kind)
	require.Equal(t, "person", computer.OwnerID)
	require.Equal(t, "build-box", computer.Name)
	require.False(t, computer.Presence.Reachable)
	require.NotContains(t, computer.Credential.Digest, redeemed.Credential, "only a digest is kept")
	authenticated, err := f.app.Authenticate(context.Background(), redeemed.Credential)
	require.NoError(t, err)
	require.Equal(t, application.Authenticated{ComputerID: redeemed.ComputerID}, authenticated, "a new credential is not due for rotation")
	_, err = f.app.Authenticate(context.Background(), redeemed.Credential+"x")
	require.ErrorIs(t, err, domain.ErrCredentialRejected)
	f.now = f.now.Add(application.CredentialLifetime)
	_, err = f.app.Authenticate(context.Background(), redeemed.Credential)
	require.ErrorIs(t, err, domain.ErrCredentialRejected, "an expired credential is rejected")
}

func TestEachRegistrationIsANewComputer(t *testing.T) {
	f := newManaged(t)
	first, second := f.register(t, "person"), f.register(t, "person")
	require.NotEqual(t, first.ComputerID, second.ComputerID)
}

func TestAnUnapprovedRegistrationExpires(t *testing.T) {
	f := newManaged(t)
	started, err := f.app.StartRegistration(context.Background(), "")
	require.NoError(t, err)
	f.redis.FastForward(application.RegistrationTTL + time.Second)
	_, err = f.app.RedeemRegistration(context.Background(), started.DeviceCode)
	require.ErrorIs(t, err, application.ErrRegistrationNotFound)
	require.ErrorIs(t, f.app.ApproveRegistration(context.Background(), started.UserCode, "person"), application.ErrRegistrationNotFound)
}

func TestPresenceFollowsTheComputersConnection(t *testing.T) {
	f := newManaged(t)
	ctx := context.Background()
	id := f.register(t, "person").ComputerID
	connection := computerconnection.Connection{ID: "conn-1", Replica: "a"}
	require.NoError(t, f.conns.Claim(ctx, id, connection))
	require.NoError(t, f.app.ReportReachable(ctx, id, application.MachineReport{Versions: domain.ReportedVersions{TalgentComputer: "0.1.0"}}))
	require.True(t, f.memory.computers[id].Presence.Reachable)
	require.Equal(t, "0.1.0", f.memory.computers[id].Versions.TalgentComputer)
	require.Equal(t, []application.PresenceChange{{ComputerID: id, Reachable: true, At: f.now}}, f.presences.published, "regaining reach is published after its commit")

	require.NoError(t, f.app.ReportUnreachable(ctx, id, f.now), "a late report of an older connection is ignored while one is open")
	require.True(t, f.memory.computers[id].Presence.Reachable)
	require.NoError(t, f.app.ReportReachable(ctx, id, application.MachineReport{Versions: domain.ReportedVersions{TalgentComputer: "0.1.0"}}))
	require.Len(t, f.presences.published, 1, "an unchanged Presence publishes nothing")

	released, err := f.conns.Release(ctx, id, connection.ID)
	require.NoError(t, err)
	require.True(t, released)
	lostAt := f.now
	require.NoError(t, f.app.ReportUnreachable(ctx, id, lostAt))
	require.Equal(t, domain.Presence{UnreachableSince: lostAt}, f.memory.computers[id].Presence)
	require.Equal(t, application.PresenceChange{ComputerID: id, Since: lostAt, At: f.now}, f.presences.published[1], "losing reach is published with its time")

	// A replica that stopped abruptly leaves its record to expire; the sweep
	// records the loss.
	require.NoError(t, f.conns.Claim(ctx, id, connection))
	require.NoError(t, f.app.ReportReachable(ctx, id, application.MachineReport{Versions: domain.ReportedVersions{TalgentComputer: "0.1.0"}}))
	lost, err := f.app.SweepPresence(ctx)
	require.NoError(t, err)
	require.Zero(t, lost)
	f.redis.FastForward(computerconnection.TTL + time.Second)
	lost, err = f.app.SweepPresence(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, lost)
	require.False(t, f.memory.computers[id].Presence.Reachable)
	require.False(t, f.presences.published[len(f.presences.published)-1].Reachable, "the sweep publishes the loss it records")

	// A loss reported late, after the machine reconnected, is put right by
	// the next sweep.
	require.NoError(t, f.conns.Claim(ctx, id, connection))
	regained, err := f.app.SweepPresence(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, regained)
	require.True(t, f.memory.computers[id].Presence.Reachable)
}

func TestAConnectionReportsItsTalgentComputerVersion(t *testing.T) {
	f := newManaged(t)
	id := f.register(t, "person").ComputerID
	require.ErrorIs(t, f.app.ReportReachable(context.Background(), id, application.MachineReport{}), domain.ErrInvalidComputer)
}

func TestOnlyItsApproverSeesARegistrationAsTheirs(t *testing.T) {
	f := newManaged(t)
	ctx := context.Background()
	started, err := f.app.StartRegistration(ctx, "box")
	require.NoError(t, err)
	shown, err := f.app.GetRegistration(ctx, started.UserCode, "person")
	require.NoError(t, err)
	require.False(t, shown.Approved)
	require.NoError(t, f.app.ApproveRegistration(ctx, started.UserCode, "person"))
	shown, err = f.app.GetRegistration(ctx, strings.ToLower(started.UserCode), "person")
	require.NoError(t, err)
	require.True(t, shown.Approved)
	_, err = f.app.GetRegistration(ctx, started.UserCode, "other")
	require.ErrorIs(t, err, application.ErrRegistrationTaken)
}

func TestARegistrationIsNotKeptWhenRedisFails(t *testing.T) {
	f := newManaged(t)
	f.redis.Close()
	_, err := f.app.StartRegistration(context.Background(), "box")
	require.Error(t, err)
	_, err = f.app.RedeemRegistration(context.Background(), "tcd_any")
	require.Error(t, err)
	require.NotErrorIs(t, err, application.ErrRegistrationNotFound, "an outage is not an expiry")
}

func TestOnlyTheOwnerRenamesAComputer(t *testing.T) {
	f := newManaged(t)
	ctx := context.Background()
	id := f.register(t, "person").ComputerID
	renamed, err := f.app.RenameComputer(ctx, "person", id, " Studio Mac ")
	require.NoError(t, err)
	require.Equal(t, "Studio Mac", renamed.Name)
	_, err = f.app.RenameComputer(ctx, "intruder", id, "mine")
	require.ErrorIs(t, err, domain.ErrComputerNotFound)
	_, err = f.app.RenameComputer(ctx, "person", id, "  ")
	require.ErrorIs(t, err, domain.ErrInvalidComputer)
	_, err = f.app.RenameComputer(ctx, "person", cloudID, "Cloud")
	require.ErrorIs(t, err, domain.ErrComputerNotFound)
}
