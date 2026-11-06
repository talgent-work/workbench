package dispatcher

import (
	"context"
	"errors"
	"sync"
	"time"

	dispatcherredis "github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/redis"
)

// takeoverConcurrency bounds how many streams this replica claims at once.
const takeoverConcurrency = 8

// Start announces this replica and begins its liveness, takeover, handover and
// input loops.
func (m *NativeExecutions) Start(ctx context.Context) error {
	sent := time.Now()
	if err := m.ownership.Heartbeat(ctx); err != nil {
		return err
	}
	m.alive.Store(sent.UnixNano())
	released := m.ownership.Released(m.ctx)
	if _, err := released.Receive(ctx); err != nil {
		_ = released.Close()
		return err
	}
	appended := m.buffer.Appended(m.ctx)
	if _, err := appended.Receive(ctx); err != nil {
		_ = released.Close()
		_ = appended.Close()
		return err
	}
	m.loops.Add(4)
	go func() {
		defer m.loops.Done()
		defer appended.Close()
		for {
			select {
			case <-m.ctx.Done():
				return
			case message, ok := <-appended.Channel():
				if !ok {
					return
				}
				m.requestFlush(message.Payload)
			}
		}
	}()
	go m.heartbeat()
	go m.scan()
	go func() {
		defer m.loops.Done()
		defer released.Close()
		for {
			select {
			case <-m.ctx.Done():
				return
			case message, ok := <-released.Channel():
				if !ok {
					return
				}
				m.takeOver(message.Payload)
			}
		}
	}()
	return nil
}

// heartbeat refreshes liveness. A replica whose last refresh was sent a
// heartbeat short of its TTL ago stops reading its streams before a peer may
// take them over; any late snapshot it writes is fenced by the generation.
func (m *NativeExecutions) heartbeat() {
	defer m.loops.Done()
	ticker := time.NewTicker(m.timing.Heartbeat.Duration())
	defer ticker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
		}
		if time.Since(time.Unix(0, m.alive.Load())) >= m.timing.TTL.Duration()-m.timing.Heartbeat.Duration() {
			m.fenceAll()
		}
		sent := time.Now()
		ctx, cancel := context.WithTimeout(m.ctx, m.timing.Heartbeat.Duration())
		err := m.ownership.Heartbeat(ctx)
		cancel()
		if err != nil {
			m.logger.Warn("dispatcher liveness refresh failed", "error", err)
			continue
		}
		m.alive.Store(sent.UnixNano())
	}
}

func (m *NativeExecutions) fenceAll() {
	m.mu.Lock()
	entries := make([]*nativeExecution, 0, len(m.connections))
	for _, entry := range m.connections {
		entries = append(entries, entry)
	}
	m.mu.Unlock()
	for _, entry := range entries {
		entry.mu.Lock()
		if entry.cancel != nil {
			entry.cancel(errFenced)
		}
		entry.mu.Unlock()
	}
}

// scan claims the streams of replicas whose liveness expired, and released
// streams nobody claimed.
func (m *NativeExecutions) scan() {
	defer m.loops.Done()
	ticker := time.NewTicker(m.timing.Scan.Duration())
	defer ticker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
		}
		ctx, cancel := context.WithTimeout(m.ctx, m.timing.Scan.Duration())
		works, err := m.ownership.Orphans(ctx)
		var owned []string
		if err == nil {
			owned, err = m.ownership.Owned(ctx)
		}
		cancel()
		if err != nil {
			m.logger.Warn("dispatcher takeover scan failed", "error", err)
			continue
		}
		// Streams still recorded as this replica's but no longer read, as after
		// fencing, are resumed too.
		for _, work := range owned {
			if !m.reading(work) {
				works = append(works, work)
			}
		}
		slots := make(chan struct{}, takeoverConcurrency)
		var claims sync.WaitGroup
		for _, work := range works {
			slots <- struct{}{}
			claims.Add(1)
			go func() {
				defer func() { <-slots; claims.Done() }()
				m.takeOver(work)
			}()
		}
		claims.Wait()
	}
}

func (m *NativeExecutions) reading(work string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.connections[work]
	return ok
}

// takeOver claims a stream another replica no longer reads. Losing the race to
// a peer is the normal outcome for all but one replica.
func (m *NativeExecutions) takeOver(work string) {
	ctx, cancel := context.WithTimeout(m.ctx, 10*time.Second)
	defer cancel()
	err := m.adopt(ctx, work, nil)
	if err != nil && !errors.Is(err, dispatcherredis.ErrOwnedElsewhere) && m.ctx.Err() == nil {
		m.logger.Warn("dispatcher stream takeover failed", "agent_work_id", work, "error", err)
	}
}

// Shutdown hands every stream to a peer: it stops reading, snapshots the
// exact position, drains feedback and releases ownership, so the peer resumes
// without replay. Removing this replica's liveness is the last step.
func (m *NativeExecutions) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	m.closed = true
	entries := make([]*nativeExecution, 0, len(m.connections))
	for _, entry := range m.connections {
		entries = append(entries, entry)
	}
	m.mu.Unlock()
	var handovers sync.WaitGroup
	for _, entry := range entries {
		handovers.Add(1)
		go func() {
			defer handovers.Done()
			m.handOver(ctx, entry)
		}()
	}
	handovers.Wait()
	m.cancel()
	done := make(chan struct{})
	go func() { m.workers.Wait(); m.loops.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		return ctx.Err()
	}
	leave, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return m.ownership.Leave(leave)
}

func (m *NativeExecutions) handOver(ctx context.Context, entry *nativeExecution) {
	entry.mu.Lock()
	reading := entry.cancel != nil
	if reading {
		entry.cancel(errHandover)
	}
	entry.mu.Unlock()
	if !reading {
		return // still claiming; adoption refuses a closed replica
	}
	select {
	case <-entry.done:
	case <-ctx.Done():
		// The peer takes the stream over once this replica's liveness expires.
		go func() { <-entry.done; m.end(entry, false) }()
		return
	}
	bounded, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	entry.mu.Lock()
	entry.GiveUpReading()
	err := m.snapshot(bounded, entry)
	entry.mu.Unlock()
	if err != nil {
		m.end(entry, false)
		return
	}
	entry.feedback.drain(bounded)
	if err := m.ownership.Release(bounded, entry.target.WorkID, entry.gen); err != nil {
		m.logger.WarnContext(bounded, "stream handover failed; a peer takes it over when this replica expires", "agent_work_id", entry.target.WorkID, "error", err)
	}
	m.end(entry, false)
}
