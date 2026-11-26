package taskqueue

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	componenttaskqueue "github.com/go-jimu/components/taskqueue"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

var (
	// ErrKeyedCoalescedTask refuses a coalesced task carrying a provider ID:
	// the ID would collapse a wake into a task already running.
	ErrKeyedCoalescedTask = errors.New("taskqueue: a coalesced task is keyed by its object, not by a task ID")
	// ErrUnboundedCoalescedRun refuses a coalesced attempt without a deadline,
	// which would hold its object for good if its process died.
	ErrUnboundedCoalescedRun = errors.New("taskqueue: a coalesced attempt needs a deadline")
)

// pendingHold is how long a wake trusts the mark of a queued task for its
// object that has not started. A mark outliving its task, as after an
// operator deleted the task, absorbs wakes until it lapses; a mark lapsing
// while its task still waits costs one more task, which does no harm.
const pendingHold = time.Minute

// holdMargin keeps an object held a little past its attempt's deadline, so an
// attempt returning at its deadline still holds the object it releases.
const holdMargin = 5 * time.Second

// settleTimeout bounds releasing an object and waking it again, which run
// after the attempt's own deadline may have passed.
const settleTimeout = 5 * time.Second

// Coalescing queues and runs tasks that reconcile one object each from its
// current state. A wake collapses only into a task for the object that has not
// started, which reads the state after the wake; the attempts for one object
// run one at a time, and a task meeting a running attempt hands its wake to
// that attempt, which wakes the object again once it releases it. A wake is
// therefore never lost to an attempt that read the state before it. A wake
// made under the attempt holding the object, as by a change the attempt
// commits itself, queues nothing: the attempt knows its own change.
//
// The marks live beside the lane's queues: per object, a pending mark while a
// task waits to start, a hold while an attempt runs, and a rerun request
// while an attempt holds the object. Each expires, so a process dying while
// holding an object delays it only until its hold lapses and the next wake,
// at the latest the periodic sweep, arrives.
type Coalescing struct {
	client   redis.UniversalClient
	enqueuer componenttaskqueue.Enqueuer
	logger   *slog.Logger
}

// NewCoalescing coalesces on the lane's Redis through its enqueuer. Its
// connections stay open until the process exits, like the enqueuer's.
func NewCoalescing(option Option, enqueuer componenttaskqueue.Enqueuer, logger *slog.Logger) *Coalescing {
	client := redis.NewClient(&redis.Options{Addr: option.RedisAddr, Password: option.RedisPassword, DB: option.RedisDB})
	return &Coalescing{client: client, enqueuer: enqueuer, logger: logger}
}

// Wake queues task for object unless a task for it waits to start, or ctx is
// the attempt holding object. The task carries no provider ID.
func (c *Coalescing) Wake(ctx context.Context, object string, task componenttaskqueue.Task, opts ...componenttaskqueue.EnqueueOption) error {
	if task.ID() != "" {
		return ErrKeyedCoalescedTask
	}
	if ctx.Value(heldObject(object)) != nil {
		return nil
	}
	keys := coalescingKeys(object)
	token := uuid.NewString()
	marked, err := c.client.SetNX(ctx, keys.pending, token, pendingHold).Result()
	if err != nil {
		return fmt.Errorf("taskqueue: mark %s pending: %w", object, err)
	}
	if !marked {
		return nil
	}
	if err := c.enqueuer.Enqueue(ctx, task, opts...); err != nil {
		// The mark covers no task: removed, so the next wake queues one.
		settle, cancel := context.WithTimeout(context.WithoutCancel(ctx), settleTimeout)
		defer cancel()
		return errors.Join(err, abandonPending.Run(settle, c.client, []string{keys.pending}, token).Err())
	}
	return nil
}

// Run takes one attempt of object's task. It runs run while holding object,
// under a context marking the hold; when another attempt holds it, run is not
// called and this task's wake is handed to the holder. After run, an attempt
// that was handed a wake, lost its hold, or cannot tell, calls rerun to wake
// object again; an error from either is returned, so the task is retried.
func (c *Coalescing) Run(ctx context.Context, object string, run, rerun func(context.Context) error) error {
	deadline, bounded := ctx.Deadline()
	if !bounded {
		return ErrUnboundedCoalescedRun
	}
	keys := coalescingKeys(object)
	token := uuid.NewString()
	hold := time.Until(deadline) + holdMargin
	held, err := holdObject.Run(ctx, c.client, keys.held(), token, hold.Milliseconds()).Int()
	if err != nil {
		return fmt.Errorf("taskqueue: hold %s: %w", object, err)
	}
	if held == 0 {
		return nil
	}
	runErr := run(context.WithValue(ctx, heldObject(object), struct{}{}))
	settle, cancel := context.WithTimeout(context.WithoutCancel(ctx), settleTimeout)
	defer cancel()
	released, err := releaseObject.Run(settle, c.client, keys.released(), token).Int()
	if err != nil {
		c.logger.WarnContext(ctx, "coalesced object release failed; waking it again", slog.String("object", object), slog.Any("error", err))
	}
	if err == nil && released == releasedAlone {
		return runErr
	}
	return errors.Join(runErr, rerun(settle))
}

// heldObject keys the context of the attempt holding an object.
type heldObject string

type objectKeys struct{ pending, hold, rerun string }

// coalescingKeys share object's hash tag, so each script touches one slot.
func coalescingKeys(object string) objectKeys {
	prefix := "taskqueue:coalescing:{" + object + "}:"
	return objectKeys{pending: prefix + "pending", hold: prefix + "hold", rerun: prefix + "rerun"}
}

func (k objectKeys) held() []string     { return []string{k.pending, k.hold, k.rerun} }
func (k objectKeys) released() []string { return []string{k.hold, k.rerun} }

// releasedAlone is releaseObject's answer when no wake was handed over.
const releasedAlone = 0

// holdObject starts an attempt: it consumes the pending mark, so a later wake
// queues a new task, and holds the object, or, when another attempt holds it,
// leaves that attempt a rerun request lasting as long as its hold. Returns 1
// when held.
var holdObject = redis.NewScript(`
redis.call('DEL', KEYS[1])
if redis.call('SET', KEYS[2], ARGV[1], 'NX', 'PX', ARGV[2]) then
  return 1
end
local ttl = redis.call('PTTL', KEYS[2])
if ttl < 1 then ttl = tonumber(ARGV[2]) end
redis.call('SET', KEYS[3], '1', 'PX', ttl)
return 0
`)

// releaseObject ends an attempt still holding the object: it releases it and
// takes the rerun request. Returns 0 when released with none, 1 when one was
// handed over, -1 when the hold had lapsed.
var releaseObject = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then
  return -1
end
local handed = redis.call('EXISTS', KEYS[2])
redis.call('DEL', KEYS[1], KEYS[2])
return handed
`)

// abandonPending removes the caller's own pending mark only.
var abandonPending = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0
`)
