package pkg

import (
	"go.uber.org/fx"

	"github.com/talgent-work/workbench/internal/pkg/messagebus"
)

// entryPoints are the process's execution entry points; a process has those
// its modules provide.
type entryPoints struct {
	fx.In

	Consumers []*messagebus.Consumer `group:"messagebus.consumers"`
}

// Lifecycle orders the entry points' hooks. List it after every bounded
// context: Fx stops hooks in reverse, so the entry points stop taking work
// and finish what runs before any context closes its Domain Event dispatcher,
// whose handlers still publish and enqueue.
var Lifecycle = fx.Module("internal.pkg.lifecycle",
	fx.Invoke(func(lc fx.Lifecycle, entries entryPoints) {
		for _, consumer := range entries.Consumers {
			lc.Append(fx.StartStopHook(consumer.Start, consumer.Stop))
		}
	}),
)
