//go:build integration

package agent_test

import (
	"sync"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/business/agent"
	pkgtaskqueue "github.com/talgent-work/workbench/internal/pkg/taskqueue"
	"github.com/talgent-work/workbench/internal/testsupport/middlewaretest"
	"go.uber.org/fx"
)

// recoveryQueues runs the production recovery task queues on the test Redis.
// Retries are fast so failure paths settle within a test's patience.
func recoveryQueues(t *testing.T) fx.Option {
	t.Helper()
	redis := recoveryRedis(t)
	queue := func(name string, workers int) pkgtaskqueue.Option {
		return pkgtaskqueue.Option{RedisAddr: redis.address, RedisDB: redis.db, QueueName: name, WorkerConcurrency: workers, RetryDelayMin: pkgtaskqueue.Duration(50 * time.Millisecond), RetryDelayMax: pkgtaskqueue.Duration(500 * time.Millisecond), ShutdownTimeout: pkgtaskqueue.Duration(5 * time.Second)}.
			// Idle workers poll every second by default; each recovery hop and each
			// restart would wait out that poll.
			WithTaskCheckInterval(10 * time.Millisecond)
	}
	return fx.Options(
		// Sweep often, so an obligation no event wakes settles within a test.
		fx.Supply(agent.RecoveryConfig{SweepInterval: agent.Duration(time.Second)}),
		fx.Supply(
			fx.Annotated{Name: agent.RecoveryQueue, Target: queue("agent-recovery", 4)},
			fx.Annotated{Name: agent.PreparationQueue, Target: queue("agent-preparation", 2)},
		),
		pkgtaskqueue.Module(agent.RecoveryQueue),
		pkgtaskqueue.Module(agent.PreparationQueue),
		// The process runs its lanes through pkg.Lifecycle; the fixture runs
		// only these.
		fx.Invoke(fx.Annotate(func(lc fx.Lifecycle, lanes []*pkgtaskqueue.Lane) {
			for _, lane := range lanes {
				lc.Append(fx.StartStopHook(lane.Start, lane.Stop))
			}
		}, fx.ParamTags("", pkgtaskqueue.LanesTag))),
	)
}

var recoveryRedisByTest sync.Map

// leasedRedis is the logical database of the shared Redis a test holds.
type leasedRedis struct {
	address string
	db      int
}

// recoveryRedis holds a database of the shared Redis once per test, which may
// compose the recovery module again to simulate a restart.
func recoveryRedis(t *testing.T) leasedRedis {
	t.Helper()
	if leased, ok := recoveryRedisByTest.Load(t); ok {
		return leased.(leasedRedis)
	}
	var leased leasedRedis
	leased.address, leased.db = middlewaretest.RedisDB(t)
	recoveryRedisByTest.Store(t, leased)
	t.Cleanup(func() { recoveryRedisByTest.Delete(t) })
	return leased
}
