package query

import (
	"errors"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// ErrStreamLimitReached refuses a stream this process cannot take now; the
// reader retries later, on this process or another.
var ErrStreamLimitReached = errors.New("live stream limit reached")

// WatchStreamOptions bound what one process serves, so a reconnect wave or one
// reader's many tabs cannot exhaust it, and pace the stream's own traffic. A
// zero field takes its default.
type WatchStreamOptions struct {
	// MaxStreams caps the streams open on this process.
	MaxStreams int `json:"max-streams" yaml:"max-streams" toml:"max-streams"`
	// MaxStreamsPerReader caps one reader's open streams on this process.
	MaxStreamsPerReader int `json:"max-streams-per-reader" yaml:"max-streams-per-reader" toml:"max-streams-per-reader"`
	// OpensPerSecond and OpenBurst pace how fast new streams open.
	OpensPerSecond float64 `json:"opens-per-second" yaml:"opens-per-second" toml:"opens-per-second"`
	OpenBurst      int     `json:"open-burst" yaml:"open-burst" toml:"open-burst"`
	// HeartbeatInterval is the longest a stream stays silent; it stays below
	// the HTTP and proxy idle timeouts.
	HeartbeatInterval time.Duration `json:"heartbeat-interval" yaml:"heartbeat-interval" toml:"heartbeat-interval"`
	// ReadmissionInterval bounds how long an open stream outlives access Agent
	// no longer grants when the withdrawal itself was lost.
	ReadmissionInterval time.Duration `json:"readmission-interval" yaml:"readmission-interval" toml:"readmission-interval"`
}

func (o WatchStreamOptions) withDefaults() WatchStreamOptions {
	if o.MaxStreams <= 0 {
		o.MaxStreams = 5000
	}
	if o.MaxStreamsPerReader <= 0 {
		o.MaxStreamsPerReader = 64
	}
	if o.OpensPerSecond <= 0 {
		o.OpensPerSecond = 50
	}
	if o.OpenBurst <= 0 {
		o.OpenBurst = 200
	}
	if o.HeartbeatInterval <= 0 {
		o.HeartbeatInterval = 30 * time.Second
	}
	if o.ReadmissionInterval <= 0 {
		o.ReadmissionInterval = time.Minute
	}
	return o
}

// streamGate admits a stream under the process's limits.
type streamGate struct {
	opens        *rate.Limiter
	maxStreams   int
	maxPerReader int

	mu        sync.Mutex
	open      int
	perReader map[string]int
}

func newStreamGate(options WatchStreamOptions) *streamGate {
	return &streamGate{
		opens:      rate.NewLimiter(rate.Limit(options.OpensPerSecond), options.OpenBurst),
		maxStreams: options.MaxStreams, maxPerReader: options.MaxStreamsPerReader,
		perReader: map[string]int{},
	}
}

// enter admits one stream of reader and returns how it leaves.
func (g *streamGate) enter(reader string) (func(), error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.open >= g.maxStreams || g.perReader[reader] >= g.maxPerReader || !g.opens.Allow() {
		return nil, ErrStreamLimitReached
	}
	g.open++
	g.perReader[reader]++
	var once sync.Once
	return func() {
		once.Do(func() {
			g.mu.Lock()
			defer g.mu.Unlock()
			g.open--
			if g.perReader[reader]--; g.perReader[reader] == 0 {
				delete(g.perReader, reader)
			}
		})
	}, nil
}
