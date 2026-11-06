package redis

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/samber/oops"

	"github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
	"github.com/talgent-work/workbench/internal/pkg/streamownership"
)

var (
	// ErrOwnedElsewhere means a live replica owns the Work's stream.
	ErrOwnedElsewhere = errors.New("dispatcher: Work stream is owned by a live replica")
	// ErrFenced means this replica no longer holds the generation it writes for.
	ErrFenced = errors.New("dispatcher: ownership generation is no longer held")
)

const (
	keyPrefix       = "dispatcher:"
	instancesKey    = keyPrefix + "instances"
	releasedKey     = keyPrefix + "released"
	ReleasedChannel = keyPrefix + "released"
)

func instanceKey(id string) string { return keyPrefix + "instance:" + id }
func worksKey(id string) string    { return keyPrefix + "works:" + id }
func ownerKey(work string) string  { return streamownership.Key(work) }
func snapshotKey(w string) string  { return keyPrefix + "snapshot:" + w }

// Owner is who reads a Work's Harness Output Stream. Instance is empty while
// the stream is released for a peer to claim; Alive reports whether the owning
// replica still refreshes its liveness. The binding is what any replica needs
// to attach, so handover and takeover need no Agent action.
type Owner struct {
	Instance, Addr string
	Generation     int64
	Binding        command.ConnectExecution
	Alive          bool
}

// Ownership is this replica's view of the replica set in Redis: its own
// liveness, per-Work ownership fenced by a generation, and boundary snapshots.
type Ownership struct {
	rdb         *goredis.Client
	self, addr  string
	ttl         time.Duration
	snapshotTTL time.Duration
}

func NewOwnership(rdb *goredis.Client, self, addr string, ttl, snapshotTTL time.Duration) *Ownership {
	return &Ownership{rdb: rdb, self: self, addr: addr, ttl: ttl, snapshotTTL: snapshotTTL}
}

func (o *Ownership) Self() string { return o.self }

// Heartbeat refreshes this replica's liveness.
func (o *Ownership) Heartbeat(ctx context.Context) error {
	pipe := o.rdb.TxPipeline()
	pipe.Set(ctx, instanceKey(o.self), o.addr, o.ttl)
	pipe.SAdd(ctx, instancesKey, o.self)
	_, err := pipe.Exec(ctx)
	return oops.Wrap(err)
}

// Leave removes this replica's liveness after it has released its Works.
func (o *Ownership) Leave(ctx context.Context) error {
	pipe := o.rdb.TxPipeline()
	pipe.Del(ctx, instanceKey(o.self))
	pipe.SRem(ctx, instancesKey, o.self)
	_, err := pipe.Exec(ctx)
	return oops.Wrap(err)
}

// Owner reads the current owner of a Work's stream.
func (o *Ownership) Owner(ctx context.Context, work string) (Owner, bool, error) {
	fields, err := o.rdb.HGetAll(ctx, ownerKey(work)).Result()
	if err != nil {
		return Owner{}, false, oops.Wrap(err)
	}
	if len(fields) == 0 {
		return Owner{}, false, nil
	}
	owner := Owner{Instance: fields["instance"], Addr: fields["addr"]}
	owner.Generation, _ = strconv.ParseInt(fields["gen"], 10, 64)
	if err := json.Unmarshal([]byte(fields["binding"]), &owner.Binding); err != nil {
		return Owner{}, false, oops.With("agent_work_id", work).Wrap(err)
	}
	if owner.Instance != "" {
		alive, err := o.rdb.Exists(ctx, instanceKey(owner.Instance)).Result()
		if err != nil {
			return Owner{}, false, oops.Wrap(err)
		}
		owner.Alive = alive == 1
	}
	return owner, true, nil
}

var _ command.ExecutionOwners = (*Ownership)(nil)

// Locate reads who reads a Work's stream, for routing commands to it.
func (o *Ownership) Locate(ctx context.Context, work string) (command.ExecutionOwner, bool, error) {
	owner, found, err := o.Owner(ctx, work)
	return command.ExecutionOwner{Instance: owner.Instance, Addr: owner.Addr, Alive: owner.Alive}, found, err
}

// claimScript takes a Work's stream unless a live other replica owns it. Every
// claim increments the generation. An empty binding keeps the stored one.
const claimScript = `
local current = redis.call("HMGET", KEYS[1], "instance", "gen", "binding")
local instance, gen, binding = current[1], tonumber(current[2] or "0"), current[3]
if instance and instance ~= "" and instance ~= ARGV[1] then
  if redis.call("EXISTS", ARGV[5] .. instance) == 1 then return {-1, ""} end
  redis.call("SREM", ARGV[6] .. instance, ARGV[4])
end
if ARGV[3] ~= "" then
  binding = ARGV[3]
  redis.call("HSET", KEYS[1], ARGV[9], ARGV[7], ARGV[10], ARGV[8])
end
if not binding then return {-2, ""} end
gen = gen + 1
redis.call("HSET", KEYS[1], "instance", ARGV[1], "addr", ARGV[2], "gen", gen, "binding", binding)
redis.call("SADD", KEYS[2], ARGV[4])
redis.call("SREM", KEYS[3], ARGV[4])
return {gen, binding}
`

// Claim takes ownership of a Work's stream with the given binding, or with the
// stored binding when none is given, as a takeover does.
func (o *Ownership) Claim(ctx context.Context, work string, binding *command.ConnectExecution) (int64, command.ConnectExecution, error) {
	encoded, process, endpoint := "", "", ""
	if binding != nil {
		process, endpoint = binding.ProcessID, binding.Endpoint
		raw, err := json.Marshal(binding)
		if err != nil {
			return 0, command.ConnectExecution{}, oops.Wrap(err)
		}
		encoded = string(raw)
	}
	result, err := o.rdb.Eval(ctx, claimScript, []string{ownerKey(work), worksKey(o.self), releasedKey},
		o.self, o.addr, encoded, work, keyPrefix+"instance:", keyPrefix+"works:", process, endpoint, streamownership.ProcessField, streamownership.EndpointField).Slice()
	if err != nil {
		return 0, command.ConnectExecution{}, oops.Wrap(err)
	}
	gen, _ := result[0].(int64)
	switch gen {
	case -1:
		return 0, command.ConnectExecution{}, ErrOwnedElsewhere
	case -2:
		return 0, command.ConnectExecution{}, oops.With("agent_work_id", work).Errorf("no binding to claim")
	}
	var claimed command.ConnectExecution
	if err := json.Unmarshal([]byte(result[1].(string)), &claimed); err != nil {
		return 0, command.ConnectExecution{}, oops.Wrap(err)
	}
	return gen, claimed, nil
}

// Fenced scripts change ownership state only for the replica holding the
// generation.
const releaseScript = `
local current = redis.call("HMGET", KEYS[1], "instance", "gen")
if current[1] ~= ARGV[1] or tonumber(current[2]) ~= tonumber(ARGV[2]) then return 0 end
redis.call("HSET", KEYS[1], "instance", "", "addr", "")
redis.call("SREM", KEYS[2], ARGV[3])
redis.call("SADD", KEYS[3], ARGV[3])
redis.call("PUBLISH", ARGV[4], ARGV[3])
return 1
`

// Release hands a Work's stream to a peer: the binding stays, the replica that
// claims it attaches from the snapshot written just before.
func (o *Ownership) Release(ctx context.Context, work string, gen int64) error {
	return o.fenced(ctx, releaseScript, []string{ownerKey(work), worksKey(o.self), releasedKey}, gen, work, ReleasedChannel)
}

const abandonScript = `
local current = redis.call("HMGET", KEYS[1], "instance", "gen")
if current[1] ~= ARGV[1] or tonumber(current[2]) ~= tonumber(ARGV[2]) then return 0 end
redis.call("DEL", KEYS[1])
redis.call("SREM", KEYS[2], ARGV[3])
return 1
`

// Abandon gives up a Work's stream without handing it over. Its snapshot lives
// until its TTL so Agent can reconnect or resume the Session in another process.
func (o *Ownership) Abandon(ctx context.Context, work string, gen int64) error {
	return o.fenced(ctx, abandonScript, []string{ownerKey(work), worksKey(o.self)}, gen, work)
}

const snapshotScript = `
local current = redis.call("HMGET", KEYS[1], "instance", "gen")
if current[1] ~= ARGV[1] or tonumber(current[2]) ~= tonumber(ARGV[2]) then return 0 end
redis.call("SET", KEYS[2], ARGV[3], "PX", ARGV[4])
return 1
`

// SaveSnapshot writes a Work's boundary snapshot; a write for a generation
// this replica no longer holds is rejected with ErrFenced.
func (o *Ownership) SaveSnapshot(ctx context.Context, work string, gen int64, snapshot []byte) error {
	return o.fenced(ctx, snapshotScript, []string{ownerKey(work), snapshotKey(work)}, gen, string(snapshot), strconv.FormatInt(o.snapshotTTL.Milliseconds(), 10))
}

func (o *Ownership) fenced(ctx context.Context, script string, keys []string, gen int64, args ...any) error {
	argv := append([]any{o.self, gen}, args...)
	applied, err := o.rdb.Eval(ctx, script, keys, argv...).Int()
	if err != nil {
		return oops.Wrap(err)
	}
	if applied != 1 {
		return ErrFenced
	}
	return nil
}

// Snapshot reads a Work's latest boundary snapshot.
func (o *Ownership) Snapshot(ctx context.Context, work string) ([]byte, bool, error) {
	raw, err := o.rdb.Get(ctx, snapshotKey(work)).Bytes()
	if errors.Is(err, goredis.Nil) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, oops.Wrap(err)
	}
	return raw, true, nil
}

// Orphans lists the Works no live replica reads: those of replicas whose
// liveness expired, and those released and not yet claimed.
func (o *Ownership) Orphans(ctx context.Context) ([]string, error) {
	instances, err := o.rdb.SMembers(ctx, instancesKey).Result()
	if err != nil {
		return nil, oops.Wrap(err)
	}
	var works []string
	for _, instance := range instances {
		if instance == o.self {
			continue
		}
		alive, err := o.rdb.Exists(ctx, instanceKey(instance)).Result()
		if err != nil {
			return nil, oops.Wrap(err)
		}
		if alive == 1 {
			continue
		}
		owned, err := o.rdb.SMembers(ctx, worksKey(instance)).Result()
		if err != nil {
			return nil, oops.Wrap(err)
		}
		works = append(works, owned...)
		if len(owned) == 0 {
			// A dead replica with nothing left to claim leaves the set.
			if err := o.rdb.SRem(ctx, instancesKey, instance).Err(); err != nil {
				return nil, oops.Wrap(err)
			}
		}
	}
	released, err := o.rdb.SMembers(ctx, releasedKey).Result()
	if err != nil {
		return nil, oops.Wrap(err)
	}
	return append(works, released...), nil
}

// Owned lists the Works this replica is recorded as reading.
func (o *Ownership) Owned(ctx context.Context) ([]string, error) {
	works, err := o.rdb.SMembers(ctx, worksKey(o.self)).Result()
	return works, oops.Wrap(err)
}

// Released subscribes to handovers; each message names a released Work. The
// caller closes the subscription.
func (o *Ownership) Released(ctx context.Context) *goredis.PubSub {
	return o.rdb.Subscribe(ctx, ReleasedChannel)
}
