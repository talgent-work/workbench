package dispatcher

import (
	"context"
	"errors"
	"time"

	"github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/nativeprocess"
	"github.com/talgent-work/workbench/internal/pkg/execd"
)

// snapshotInterval bounds how much a replay repeats when nothing else marked a
// boundary: a changed state is snapshotted at least this often.
const snapshotInterval = 30 * time.Second

func (m *NativeExecutions) run(entry *nativeExecution, client *execd.Client) {
	defer m.workers.Done()
	defer close(entry.done)
	ctx, cancel := context.WithCancelCause(m.ctx)
	defer cancel(nil)
	entry.mu.Lock()
	entry.cancel = cancel
	entry.mu.Unlock()
	go m.snapshotPeriodically(ctx, entry)
	err := m.consume(ctx, entry, client)
	entry.mu.Lock()
	held := entry.attached
	entry.attached, entry.broken = false, true
	for id, response := range entry.settings {
		response <- settingResult{err: command.ErrExecutionNotReady}
		delete(entry.settings, id)
	}
	exited := entry.Exited
	entry.mu.Unlock()
	cause := context.Cause(ctx)
	switch {
	case errors.Is(cause, errHandover):
		// Shutdown snapshots, drains feedback and releases the stream.
		return
	case errors.Is(cause, errFenced), m.ctx.Err() != nil:
		// Another replica owns the stream now; this one writes nothing more.
		m.end(entry, false)
		return
	case exited && err == nil:
		m.awaitExitAcknowledgement(ctx, entry)
		return
	}
	if cause != nil {
		err = cause
	}
	// The sandbox side failed. Keep the snapshot so the next Connect resumes,
	// give the stream up and let Agent decide whether to connect again.
	m.logger.InfoContext(context.Background(), "native execution connection ended", "agent_work_id", entry.target.WorkID, "process_id", entry.target.ProcessID, "error", err)
	entry.mu.Lock()
	m.attachmentEnded(entry, held, time.Now())
	entry.mu.Unlock()
	m.endAbandoned(entry)
	m.reportConnectionEnded(entry.target)
}

// awaitExitAcknowledgement gives the stream up once Agent applied the exit
// fact. A handover or takeover before that redelivers it from the snapshot.
func (m *NativeExecutions) awaitExitAcknowledgement(ctx context.Context, entry *nativeExecution) {
	select {
	case <-entry.feedback.exited:
	case <-ctx.Done():
		if errors.Is(context.Cause(ctx), errHandover) {
			return
		}
		m.end(entry, false)
		return
	}
	entry.mu.Lock()
	entry.ExitAcknowledged = true
	entry.mu.Unlock()
	release, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	// The process ended, but the Harness Session can resume in a new process.
	// Retain its task state and attribution until the snapshot TTL.
	if err := m.ownership.Abandon(release, entry.target.WorkID, entry.gen); err != nil {
		m.logger.WarnContext(release, "acknowledged stream was not released", "agent_work_id", entry.target.WorkID, "error", err)
	}
	m.end(entry, true)
}

func (m *NativeExecutions) endAbandoned(entry *nativeExecution) {
	release, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	entry.mu.Lock()
	entry.GiveUpReading()
	_ = m.snapshot(release, entry)
	entry.mu.Unlock()
	entry.feedback.drain(release)
	if err := m.ownership.Abandon(release, entry.target.WorkID, entry.gen); err != nil {
		m.logger.WarnContext(release, "ended stream was not given up", "agent_work_id", entry.target.WorkID, "error", err)
	}
	m.end(entry, true)
}

// end forgets an entry; delivered drains its feedback first.
func (m *NativeExecutions) end(entry *nativeExecution, drainFeedback bool) {
	if drainFeedback {
		drain, done := context.WithTimeout(context.Background(), 5*time.Second)
		entry.feedback.drain(drain)
		done()
	}
	entry.feedback.close()
	m.forget(entry, entry.target.WorkID)
}

// reportConnectionEnded tells Agent the sandbox side of a still running
// process failed, so Agent can connect again. A lost report is recovered by
// Agent's sweep, which finds the Work without a live owner.
func (m *NativeExecutions) reportConnectionEnded(target command.ConnectExecution) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := m.feedback.ReportConnectionEnded(ctx, target.WorkID, target.ProcessID); err != nil {
		m.logger.WarnContext(ctx, "native execution connection end was not reported", "agent_work_id", target.WorkID, "process_id", target.ProcessID, "error", err)
	}
}

func (m *NativeExecutions) snapshotPeriodically(ctx context.Context, entry *nativeExecution) {
	ticker := time.NewTicker(snapshotInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			entry.mu.Lock()
			if entry.dirty && !entry.broken {
				_ = m.snapshot(ctx, entry)
			}
			entry.mu.Unlock()
		}
	}
}

func (m *NativeExecutions) consume(ctx context.Context, entry *nativeExecution, client *execd.Client) error {
	entry.mu.Lock()
	if entry.Exited {
		// Resumed after the exit, before Agent acknowledged it.
		err := m.reportExit(ctx, entry)
		entry.mu.Unlock()
		return err
	}
	offset := entry.Offset
	entry.mu.Unlock()
	startup, done := context.WithTimeout(ctx, 30*time.Second)
	defer done()
	pipe, err := client.Attach(startup, entry.target.ProcessID, offset)
	if err != nil {
		return err
	}
	connection := nativeprocess.NewConnection(pipe, entry.target.NativeSessionID, offset)
	defer connection.Close()
	entry.mu.Lock()
	entry.connection = connection
	entry.mu.Unlock()
	initializeID := "initialize-" + entry.target.ProcessID
	for {
		entry.mu.Lock()
		ready := entry.Ready
		entry.mu.Unlock()
		readContext := ctx
		if !ready {
			readContext = startup
		}
		event, err := connection.Receive(readContext)
		if err != nil {
			return err
		}
		entry.mu.Lock()
		err = m.applyEvent(ctx, startup, entry, initializeID, event)
		entry.mu.Unlock()
		if err != nil {
			return err
		}
		if event.Kind == "exit" {
			return nil
		}
	}
}

// report queues an observation for Agent; the caller holds entry.mu.
func (m *NativeExecutions) report(entry *nativeExecution, observation command.ExecutionObservation) {
	observation.RequiredInteractionIDs = entry.PendingInteractions()
	if observation.ObservationSequence == 0 {
		observation.ObservationSequence = entry.Offset
	}
	observation.WorkID, observation.ProcessID = entry.target.WorkID, entry.target.ProcessID
	entry.feedback.push(observation)
}
