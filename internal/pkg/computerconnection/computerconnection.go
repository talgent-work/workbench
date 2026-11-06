// Package computerconnection is the published contract for the Computer
// Gateway's connection records: which Gateway replica holds a
// Managed Computer's one outbound connection, and where to forward to it.
// The Gateway writes the record; Computer and the execution port only read
// it here. A record lives while its connection heartbeats; a replica that
// stops abruptly leaves it to expire.
package computerconnection

import (
	"context"
	"errors"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/samber/oops"
)

// TTL is how long a record outlives its connection's last heartbeat.
const TTL = 90 * time.Second

const (
	connectionField = "connection_id"
	replicaField    = "replica"
	endpointField   = "endpoint"
)

// Key names a Computer's connection record.
func Key(computerID string) string { return "computer:connection:" + computerID }

// Connection is one connection a Gateway replica holds.
type Connection struct {
	// ID identifies this connection among every connection of the Computer.
	ID string
	// Replica names the Gateway replica holding it.
	Replica string
	// Endpoint is where that replica takes forwarded calls for it.
	Endpoint string
}

// Reader reads connection records.
type Reader struct{ rdb goredis.UniversalClient }

func NewReader(rdb goredis.UniversalClient) *Reader { return &Reader{rdb: rdb} }

// Lookup returns the connection a Computer holds now, if any.
func (r *Reader) Lookup(ctx context.Context, computerID string) (Connection, bool, error) {
	values, err := r.rdb.HMGet(ctx, Key(computerID), connectionField, replicaField, endpointField).Result()
	if err != nil {
		return Connection{}, false, oops.Wrap(err)
	}
	id, _ := values[0].(string)
	if id == "" {
		return Connection{}, false, nil
	}
	replica, _ := values[1].(string)
	endpoint, _ := values[2].(string)
	return Connection{ID: id, Replica: replica, Endpoint: endpoint}, true, nil
}

// Connected tells which of the Computers hold a connection now.
func (r *Reader) Connected(ctx context.Context, computerIDs []string) (map[string]bool, error) {
	pipe := r.rdb.Pipeline()
	checks := make([]*goredis.IntCmd, len(computerIDs))
	for i, id := range computerIDs {
		checks[i] = pipe.Exists(ctx, Key(id))
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, goredis.Nil) {
		return nil, oops.Wrap(err)
	}
	connected := make(map[string]bool, len(computerIDs))
	for i, id := range computerIDs {
		connected[id] = checks[i].Val() == 1
	}
	return connected, nil
}

// Refresh is what a heartbeat found of its connection's record.
type Refresh int

const (
	// Held: the record is this connection's and lives on.
	Held Refresh = iota
	// Reclaimed: the record had expired and is this connection's again.
	Reclaimed
	// Replaced: a newer connection of the Computer holds the record.
	Replaced
)

// Writer is the Gateway's side of the records.
type Writer struct{ rdb goredis.UniversalClient }

func NewWriter(rdb goredis.UniversalClient) *Writer { return &Writer{rdb: rdb} }

// Claim records a new connection as the Computer's; the newest connection
// wins, and the replica holding an older one finds it Replaced.
func (w *Writer) Claim(ctx context.Context, computerID string, connection Connection) error {
	pipe := w.rdb.TxPipeline()
	pipe.Del(ctx, Key(computerID))
	pipe.HSet(ctx, Key(computerID), connectionField, connection.ID, replicaField, connection.Replica, endpointField, connection.Endpoint)
	pipe.PExpire(ctx, Key(computerID), TTL)
	_, err := pipe.Exec(ctx)
	return oops.Wrap(err)
}

var refreshScript = goredis.NewScript(`
local current = redis.call('HGET', KEYS[1], 'connection_id')
if current == ARGV[1] then
  redis.call('PEXPIRE', KEYS[1], ARGV[4])
  return 0
end
if not current then
  redis.call('HSET', KEYS[1], 'connection_id', ARGV[1], 'replica', ARGV[2], 'endpoint', ARGV[3])
  redis.call('PEXPIRE', KEYS[1], ARGV[4])
  return 1
end
return 2`)

// Refresh extends this connection's record on a heartbeat, taking it back
// when it expired, unless a newer connection holds it.
func (w *Writer) Refresh(ctx context.Context, computerID string, connection Connection) (Refresh, error) {
	result, err := refreshScript.Run(ctx, w.rdb, []string{Key(computerID)}, connection.ID, connection.Replica, connection.Endpoint, TTL.Milliseconds()).Int()
	if err != nil {
		return Held, oops.Wrap(err)
	}
	return Refresh(result), nil
}

var releaseScript = goredis.NewScript(`
if redis.call('HGET', KEYS[1], 'connection_id') == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0`)

// Release forgets this connection's record when it still holds it, and
// reports whether it did.
func (w *Writer) Release(ctx context.Context, computerID, connectionID string) (bool, error) {
	released, err := releaseScript.Run(ctx, w.rdb, []string{Key(computerID)}, connectionID).Int()
	if err != nil {
		return false, oops.Wrap(err)
	}
	return released == 1, nil
}
