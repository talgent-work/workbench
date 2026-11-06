// Package messagepublisher publishes Computer's Integration Messages.
package messagepublisher

import (
	"context"
	"log/slog"
	"sync"
	"time"

	integrationmessage "github.com/go-jimu/components/ddd/message"
	"google.golang.org/protobuf/proto"
)

const (
	publishTimeout = 5 * time.Second
	// maxPublishing bounds the publications under way at once; a change
	// beyond it is not published and Agent's own reading finds it.
	maxPublishing = 8
)

// publications runs best-effort publications after their change committed,
// for the application's lifetime: each never delays or fails its change, at
// most maxPublishing run at once, and stop ends them. Every fact Computer
// publishes this way is one Agent also reads back, so a lost one only delays
// its consumer.
type publications struct {
	bus     integrationmessage.Publisher
	logger  *slog.Logger
	timeout time.Duration
	slots   chan struct{}

	mu       sync.Mutex
	stopped  bool
	inflight sync.WaitGroup
	lifetime context.Context
	end      context.CancelFunc
}

func newPublications(bus integrationmessage.Publisher, logger *slog.Logger, component string) *publications {
	if logger == nil {
		logger = slog.Default()
	}
	lifetime, end := context.WithCancel(context.Background())
	return &publications{
		bus: bus, logger: logger.With("component", component), timeout: publishTimeout,
		slots: make(chan struct{}, maxPublishing), lifetime: lifetime, end: end,
	}
}

// fact is one Integration Message to publish, keyed and described for logs.
type fact struct {
	payload proto.Message
	key     string
	attrs   []any
}

// publish sends the facts in order in the background; what describes them
// is logged when they cannot be published at all.
func (p *publications) publish(parent context.Context, what string, facts []fact) {
	if p == nil || p.bus == nil || len(facts) == 0 {
		return
	}
	if !p.begin() {
		p.logger.WarnContext(parent, what+" not published", slog.Int("facts", len(facts)), slog.String("reason", "publisher stopped or busy"))
		return
	}
	go func() {
		defer p.done()
		ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), p.timeout)
		defer cancel()
		defer context.AfterFunc(p.lifetime, cancel)()
		for _, f := range facts {
			p.send(ctx, what, f)
		}
	}()
}

func (p *publications) send(ctx context.Context, what string, f fact) {
	msg, err := integrationmessage.New(integrationmessage.KindOf(f.payload), f.payload, integrationmessage.WithKey(f.key))
	if err == nil {
		err = p.bus.Publish(ctx, msg)
	}
	if err != nil {
		p.logger.WarnContext(ctx, "publish "+what+" failed", append(f.attrs, slog.Any("error", err))...)
		return
	}
	p.logger.InfoContext(ctx, what+" published", f.attrs...)
}

// begin takes a publishing slot unless publishing stopped or every slot is
// taken.
func (p *publications) begin() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped {
		return false
	}
	select {
	case p.slots <- struct{}{}:
		p.inflight.Add(1)
		return true
	default:
		return false
	}
}

func (p *publications) done() {
	<-p.slots
	p.inflight.Done()
}

// stop starts no further publication and waits for those under way until
// ctx ends, then cancels them.
func (p *publications) stop(ctx context.Context) error {
	p.mu.Lock()
	p.stopped = true
	p.mu.Unlock()
	finished := make(chan struct{})
	go func() {
		p.inflight.Wait()
		close(finished)
	}()
	defer p.end()
	select {
	case <-finished:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
