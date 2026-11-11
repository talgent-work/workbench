package dispatcher

// The input buffer protocol: Agent hands accepted input to a Work's
// buffer in Redis, and the replica reading the Work's stream writes it to the
// Harness. Entries stay buffered until the Harness settles them.

import (
	"context"
	"encoding/json"
	"time"

	"github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/adapter"
	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/nativeprocess"
	dispatcherredis "github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/redis"
)

const bufferTimeout = 5 * time.Second

type settlement struct {
	entry   command.BufferedInput
	outcome command.InputOutcome
}

// mirror replaces the reader's copy of the buffer, whose source of truth is
// Redis; the caller holds e.mu.
func (e *nativeExecution) mirror(entries []dispatcherredis.InputEntry) {
	e.buffered = entries
	inputs := make([]command.BufferedInput, len(entries))
	for i, entry := range entries {
		inputs[i] = entry.BufferedInput
	}
	e.MirrorBuffer(inputs)
}

// cancelling reports a requested cancellation of the running Turn that the
// Harness has not acknowledged; input written now would join that Turn.
func (e *nativeExecution) cancelling() bool {
	return e.Active && e.CancelTurnID != "" && !e.CancelAck && e.Projection.CurrentTurnID == e.CancelTurnID
}

// flushRetry is how soon a flush that could not read the buffer runs again;
// an idle Work has no other trigger. flushRetries bounds how often in a row:
// a flush that exhausts them is given up with an error, and the input stays
// due for the Work's next trigger (new input, readiness or a Turn's end).
const (
	flushRetry   = time.Second
	flushRetries = 30
)

// flush writes every buffered entry the current connection has not written,
// in order and back to back, without waiting for any to be processed. It runs
// only while attached, ready, caught up with replay and not cancelling a Turn;
// otherwise the flush stays due for the next trigger. The caller holds e.mu.
func (m *NativeExecutions) flush(ctx context.Context, e *nativeExecution) error {
	if e.ProtocolState == nil || e.broken || !e.attached || !e.Ready || e.Exited || e.Failure != "" || e.cancelling() {
		e.flushDue = true
		return nil
	}
	e.flushDue = false
	bounded, cancel := context.WithTimeout(context.WithoutCancel(ctx), bufferTimeout)
	defer cancel()
	m.retrySettlements(bounded, e)
	entries, err := m.buffer.Entries(bounded, e.target.WorkID)
	if err != nil {
		e.flushDue = true
		if e.flushFailures >= flushRetries {
			e.flushFailures = 0
			m.logger.ErrorContext(ctx, "input buffer flush given up", "agent_work_id", e.target.WorkID, "retries", flushRetries, "error", err)
			return nil
		}
		e.flushFailures++
		m.logger.WarnContext(ctx, "input buffer read failed", "agent_work_id", e.target.WorkID, "error", err)
		time.AfterFunc(flushRetry, func() { m.requestFlush(e.target.WorkID) })
		return nil
	}
	e.flushFailures = 0
	e.mirror(entries)
	var written []string
	// Entries written before a failed write are recorded too, so whichever
	// replica resumes the stream knows they reached the process.
	defer func() {
		// Its own budget: a write that timed out must not take the record with it.
		record, done := context.WithTimeout(context.WithoutCancel(ctx), bufferTimeout)
		defer done()
		if err := m.buffer.MarkWritten(record, e.target.WorkID, written); err != nil {
			m.logger.WarnContext(ctx, "written input was not recorded", "agent_work_id", e.target.WorkID, "error", err)
		}
	}()
	for _, entry := range entries {
		key := entry.Key()
		if e.written[key] {
			continue
		}
		if entry.Reply != nil {
			request, pending := e.Pending[entry.ID]
			var response nativeprocess.ToolResponse
			if pending {
				reply := entry.Reply
				response, err = nativeprocess.NewToolResponse(request, reply.Allow, reply.AlwaysAllow, reply.Reason, json.RawMessage(reply.UpdatedInputJSON))
			}
			// Caught up with replay, a reply whose interaction is gone, or one the
			// interaction cannot take, can never be written.
			if !pending || err != nil {
				if m.settle(bounded, e, entry.BufferedInput, command.InputClosed) {
					m.publishNotice(ctx, e.target.WorkID, adapter.InteractionClosed())
				}
				continue
			}
			if err := e.connection.ReplyTool(bounded, response); err != nil {
				return e.disconnect(err)
			}
		} else {
			if err := e.connection.UserInput(bounded, entry.ID, entry.Text); err != nil {
				return e.disconnect(err)
			}
			// Written input is active work until its Turn reports otherwise. A
			// rewrite of input the process already has is deduplicated by the
			// Harness and starts no Turn; whether that input's Turn still runs is
			// the stream's to say, so a rewrite does not mark one active.
			if !entry.Written {
				e.Active = true
			}
		}
		e.written[key] = true
		if !entry.Written {
			written = append(written, key)
		}
	}
	return nil
}

// settle removes an entry from the buffer and reports its outcome to Agent
// once removed, so Agent's next read of the buffer agrees with the report. A
// failed removal is retried by the next flush. It reports whether this call
// removed the entry. The caller holds e.mu.
func (m *NativeExecutions) settle(ctx context.Context, e *nativeExecution, entry command.BufferedInput, outcome command.InputOutcome) bool {
	key := dispatcherredis.InputEntry{BufferedInput: entry}.Key()
	removed, err := m.buffer.Settle(ctx, e.target.WorkID, key)
	if err != nil {
		e.unsettled[key] = settlement{entry: entry, outcome: outcome}
		m.logger.WarnContext(ctx, "settled input was not removed", "agent_work_id", e.target.WorkID, "error", err)
		return false
	}
	delete(e.unsettled, key)
	delete(e.written, key)
	e.ForgetBuffered(entry)
	if removed {
		m.report(e, command.ExecutionObservation{Kind: command.ExecutionInputSettled, InputID: entry.ID, InputOutcome: outcome})
	}
	return removed
}

func (m *NativeExecutions) retrySettlements(ctx context.Context, e *nativeExecution) {
	for _, pending := range e.unsettled {
		m.settle(ctx, e, pending.entry, pending.outcome)
	}
}

// settleBuffered settles the mirrored entry with the identity, if buffered.
func (m *NativeExecutions) settleBuffered(ctx context.Context, e *nativeExecution, id string, reply bool, outcome command.InputOutcome) {
	entries := e.Inputs
	if reply {
		entries = e.Replies
	}
	entry, buffered := entries[id]
	if !buffered {
		return
	}
	bounded, cancel := context.WithTimeout(context.WithoutCancel(ctx), bufferTimeout)
	defer cancel()
	m.settle(bounded, e, entry, outcome)
}

// settleAtExit settles what the exited process held: written input it did not
// finish is abandoned and every reply is closed. Unwritten input waits for
// the next process. The caller holds e.mu.
func (m *NativeExecutions) settleAtExit(ctx context.Context, e *nativeExecution) {
	bounded, cancel := context.WithTimeout(context.WithoutCancel(ctx), bufferTimeout)
	defer cancel()
	for _, entry := range e.buffered {
		switch {
		case entry.Reply != nil:
			m.settle(bounded, e, entry.BufferedInput, command.InputClosed)
		case entry.Written || e.written[entry.Key()]:
			m.settle(bounded, e, entry.BufferedInput, command.InputAbandoned)
		}
	}
}

// requestFlush follows a change to a Work's buffer when this replica reads
// the Work's stream. Changes arriving while a flush is queued share it, and
// the flush is tracked so shutdown waits for it.
func (m *NativeExecutions) requestFlush(workID string) {
	m.mu.Lock()
	entry, reading := m.connections[workID]
	if !reading || m.closed || !entry.flushQueued.CompareAndSwap(false, true) {
		m.mu.Unlock()
		return
	}
	m.workers.Add(1)
	m.mu.Unlock()
	go func() {
		defer m.workers.Done()
		select {
		case <-entry.claimed:
		case <-m.ctx.Done():
			entry.flushQueued.Store(false)
			return
		}
		entry.flushQueued.Store(false)
		if entry.claimErr != nil {
			return
		}
		entry.mu.Lock()
		defer entry.mu.Unlock()
		_ = m.flush(m.ctx, entry)
	}()
}
