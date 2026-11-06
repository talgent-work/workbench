package redis_test

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
	dispatcherredis "github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/redis"
	"github.com/talgent-work/workbench/internal/pkg/streamownership"
)

func replicas(t *testing.T) (*miniredis.Miniredis, *dispatcherredis.Ownership, *dispatcherredis.Ownership) {
	t.Helper()
	server := miniredis.RunT(t)
	replica := func(id, addr string) *dispatcherredis.Ownership {
		o := dispatcherredis.NewOwnership(goredis.NewClient(&goredis.Options{Addr: server.Addr()}), id, addr, 6*time.Second, time.Hour)
		require.NoError(t, o.Heartbeat(context.Background()))
		return o
	}
	return server, replica("a", "192.0.2.1:8083"), replica("b", "192.0.2.2:8083")
}

var binding = command.ConnectExecution{WorkID: "work", Endpoint: "http://sandbox", AccessToken: "token", ProcessID: "process", NativeSessionID: "session"}

// Intent (real): only one live replica owns a Work's stream; each claim moves
// the generation, and a write for an earlier generation is fenced.
func TestClaimIsExclusiveAndGenerationsFenceWrites(t *testing.T) {
	ctx := context.Background()
	_, a, b := replicas(t)
	gen, claimed, err := a.Claim(ctx, "work", &binding)
	require.NoError(t, err)
	require.EqualValues(t, 1, gen)
	require.Equal(t, binding, claimed)
	_, _, err = b.Claim(ctx, "work", &binding)
	require.ErrorIs(t, err, dispatcherredis.ErrOwnedElsewhere)

	owner, found, err := b.Owner(ctx, "work")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, dispatcherredis.Owner{Instance: "a", Addr: "192.0.2.1:8083", Generation: 1, Binding: binding, Alive: true}, owner)

	require.NoError(t, a.SaveSnapshot(ctx, "work", gen, []byte(`{"offset":10}`)))
	regen, _, err := a.Claim(ctx, "work", &binding)
	require.NoError(t, err)
	require.EqualValues(t, 2, regen)
	require.ErrorIs(t, a.SaveSnapshot(ctx, "work", gen, []byte(`{"offset":5}`)), dispatcherredis.ErrFenced, "a late write of an earlier generation is rejected")
	snapshot, found, err := b.Snapshot(ctx, "work")
	require.NoError(t, err)
	require.True(t, found)
	require.JSONEq(t, `{"offset":10}`, string(snapshot))
}

// Intent (real): a replica whose liveness expired loses its Works to a
// survivor, which claims them with the stored binding; the expired replica's
// late snapshot is rejected.
func TestSurvivorTakesOverAnExpiredReplica(t *testing.T) {
	ctx := context.Background()
	server, a, b := replicas(t)
	gen, _, err := a.Claim(ctx, "work", &binding)
	require.NoError(t, err)
	orphans, err := b.Orphans(ctx)
	require.NoError(t, err)
	require.Empty(t, orphans, "a live replica's Works are not orphans")

	server.FastForward(7 * time.Second)
	require.NoError(t, b.Heartbeat(ctx))
	orphans, err = b.Orphans(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"work"}, orphans)
	taken, claimed, err := b.Claim(ctx, "work", nil)
	require.NoError(t, err)
	require.Equal(t, gen+1, taken)
	require.Equal(t, binding, claimed, "takeover attaches with the stored binding")
	require.ErrorIs(t, a.SaveSnapshot(ctx, "work", gen, []byte(`{}`)), dispatcherredis.ErrFenced)

	orphans, err = b.Orphans(ctx)
	require.NoError(t, err)
	require.Empty(t, orphans)
}

// Intent (real): a released stream keeps its binding for a peer to claim,
// and stays an orphan until one does.
func TestReleasedStreamIsClaimedByAPeer(t *testing.T) {
	ctx := context.Background()
	_, a, b := replicas(t)
	gen, _, err := a.Claim(ctx, "work", &binding)
	require.NoError(t, err)
	subscription := b.Released(ctx)
	defer subscription.Close()
	_, err = subscription.Receive(ctx)
	require.NoError(t, err)

	require.NoError(t, a.Release(ctx, "work", gen))
	message := <-subscription.Channel()
	require.Equal(t, "work", message.Payload)
	orphans, err := b.Orphans(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"work"}, orphans)
	owner, _, err := b.Owner(ctx, "work")
	require.NoError(t, err)
	require.False(t, owner.Alive)
	require.ErrorIs(t, a.Release(ctx, "work", gen), dispatcherredis.ErrFenced, "a released stream is no longer held")

	_, claimed, err := b.Claim(ctx, "work", nil)
	require.NoError(t, err)
	require.Equal(t, binding, claimed)
	orphans, err = b.Orphans(ctx)
	require.NoError(t, err)
	require.Empty(t, orphans)
}

// Releasing a process does not end its Session; its snapshot remains available
// to another process, bounded by the configured retention time.
func TestAbandonKeepsTheSnapshotUntilItsTTL(t *testing.T) {
	ctx := context.Background()
	server, a, b := replicas(t)
	gen, _, err := a.Claim(ctx, "work", &binding)
	require.NoError(t, err)
	require.NoError(t, a.SaveSnapshot(ctx, "work", gen, []byte(`{"offset":42}`)))
	require.NoError(t, a.Abandon(ctx, "work", gen))
	_, owned, err := b.Owner(ctx, "work")
	require.NoError(t, err)
	require.False(t, owned)
	snapshot, kept, err := b.Snapshot(ctx, "work")
	require.NoError(t, err)
	require.True(t, kept)
	require.JSONEq(t, `{"offset":42}`, string(snapshot))
	server.FastForward(time.Hour)
	_, kept, err = b.Snapshot(ctx, "work")
	require.NoError(t, err)
	require.False(t, kept, "ended Sessions must not retain snapshots indefinitely")
}

func TestPublishedReadingFollowsTheClaimedBindingUntilForgotten(t *testing.T) {
	ctx := context.Background()
	server := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: server.Addr()})
	reader := streamownership.NewReader(rdb)
	_, found, err := reader.Read(ctx, "work")
	require.NoError(t, err)
	require.False(t, found)

	replica := dispatcherredis.NewOwnership(rdb, "a", "192.0.2.1:9000", time.Minute, time.Hour)
	require.NoError(t, replica.Heartbeat(ctx))
	binding := command.ConnectExecution{WorkID: "work", ProcessID: "process", Endpoint: "http://sandbox:44772", NativeSessionID: "session"}
	gen, _, err := replica.Claim(ctx, "work", &binding)
	require.NoError(t, err)
	read, found, err := reader.Read(ctx, "work")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, streamownership.Reading{ProcessID: "process", Endpoint: "http://sandbox:44772"}, read)

	// A takeover keeps the published reading.
	peer := dispatcherredis.NewOwnership(rdb, "b", "192.0.2.2:9000", time.Minute, time.Hour)
	require.NoError(t, replica.Release(ctx, "work", gen))
	gen, _, err = peer.Claim(ctx, "work", nil)
	require.NoError(t, err)
	read, _, err = reader.Read(ctx, "work")
	require.NoError(t, err)
	require.Equal(t, "process", read.ProcessID)

	require.NoError(t, peer.Abandon(ctx, "work", gen))
	_, found, err = reader.Read(ctx, "work")
	require.NoError(t, err)
	require.False(t, found, "a forgotten stream has no reader")
}
