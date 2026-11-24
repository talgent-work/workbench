package pkg

import (
	"go.uber.org/fx"

	"github.com/talgent-work/workbench/internal/pkg/connectrpc"
	"github.com/talgent-work/workbench/internal/pkg/messagebus"
	"github.com/talgent-work/workbench/internal/pkg/taskqueue"
)

// entryPoints are the process's execution entry points; a process has those
// its modules provide.
type entryPoints struct {
	fx.In

	Server    *connectrpc.HTTPServer `optional:"true"`
	Consumers []*messagebus.Consumer `group:"messagebus.consumers"`
	Lanes     []*taskqueue.Lane      `group:"taskqueue.lanes"`
}

// Lifecycle orders the entry points' hooks. List it after every bounded
// context: Fx stops hooks in reverse, so the entry points stop taking work
// and finish what runs before any context closes its Domain Event dispatcher,
// whose handlers still publish and enqueue. The listener starts last, so the
// process is ready only once every other entry point runs, and stops first.
var Lifecycle = fx.Module("internal.pkg.lifecycle",
	fx.Invoke(func(lc fx.Lifecycle, entries entryPoints) {
		for _, consumer := range entries.Consumers {
			lc.Append(fx.StartStopHook(consumer.Start, consumer.Stop))
		}
		for _, lane := range entries.Lanes {
			lc.Append(fx.StartStopHook(lane.Start, lane.Stop))
		}
		if entries.Server != nil {
			lc.Append(fx.StartStopHook(entries.Server.Start, entries.Server.Stop))
		}
	}),
)
