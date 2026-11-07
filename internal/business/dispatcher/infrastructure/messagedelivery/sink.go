// Package messagedelivery is how UnifiedMessages reach Work Message Stream
// readers: the message log for history and NATS for live readers.
package messagedelivery

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	dispatcherpublicv1 "github.com/talgent-work/contracts/gen/dispatcher/public/v1"
)

const (
	failureStorageUnavailable = "storage_unavailable"
	failureLiveUnavailable    = "live_delivery_unavailable"

	// A batch reaches the message log when it holds batchSize messages or has
	// waited batchInterval.
	batchSize     = 500
	batchInterval = 200 * time.Millisecond
	queueCapacity = 8192
)

var ErrSinkClosed = errors.New("dispatcher: message delivery is closed")

// MessageLog appends UnifiedMessages, details included, to history.
type MessageLog interface {
	Append(ctx context.Context, messages ...*dispatcherpublicv1.UnifiedMessage) error
}

// LivePublisher tells live readers a UnifiedMessage without its detail.
type LivePublisher interface {
	PublishLive(ctx context.Context, msg *dispatcherpublicv1.UnifiedMessage) error
}

type DiagnosticPublisher interface {
	PublishDiagnostic(ctx context.Context, diagnostic Diagnostic) error
}

type delivery struct {
	message *dispatcherpublicv1.UnifiedMessage
	// appended reports the synchronous append of a turn RESULT.
	appended chan error
}

// Sink delivers UnifiedMessages without making their producer wait: a message
// is published live and batched for the message log. Delivery is best effort
// except a turn RESULT, which DeliverResult appends before it
// returns, so a turn is reported completed only after its RESULT is stored.
type Sink struct {
	log         MessageLog
	live        LivePublisher
	diagnostics DiagnosticPublisher
	logger      *slog.Logger
	queue       chan delivery
	// published feeds live publication apart from batching, so a slow live
	// path never delays the message log.
	published chan *dispatcherpublicv1.UnifiedMessage
	closeOnce sync.Once
	closed    chan struct{}
	done      chan struct{}
}

func NewSink(log MessageLog, live LivePublisher, diagnostics DiagnosticPublisher, logger *slog.Logger) (*Sink, error) {
	if log == nil || live == nil || diagnostics == nil || logger == nil {
		return nil, errors.New("dispatcher: message delivery dependencies required")
	}
	s := &Sink{log: log, live: live, diagnostics: diagnostics, logger: logger.With("component", "dispatcher.message_delivery"),
		queue: make(chan delivery, queueCapacity), published: make(chan *dispatcherpublicv1.UnifiedMessage, queueCapacity), closed: make(chan struct{}), done: make(chan struct{})}
	go s.run()
	return s, nil
}

// Deliver hands a message over without waiting. A message that finds the queue
// full is dropped and reported live, as best-effort delivery allows.
func (s *Sink) Deliver(msg *dispatcherpublicv1.UnifiedMessage) {
	if msg == nil {
		return
	}
	select {
	case <-s.closed:
		return
	default:
	}
	select {
	case s.queue <- delivery{message: msg}:
	default:
		s.publishFailure(context.Background(), []*dispatcherpublicv1.UnifiedMessage{msg}, failureStorageUnavailable, errors.New("delivery queue is full"))
	}
}

// DeliverResult appends a turn RESULT, with every message delivered before
// it, and returns once the append is done. Its error means the RESULT is not
// stored.
func (s *Sink) DeliverResult(ctx context.Context, msg *dispatcherpublicv1.UnifiedMessage) error {
	appended := make(chan error, 1)
	select {
	case <-s.closed:
		return ErrSinkClosed
	default:
	}
	select {
	case s.queue <- delivery{message: msg, appended: appended}:
	case <-s.closed:
		return ErrSinkClosed
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-appended:
		return err
	case <-s.done:
		// The sink stopped; the RESULT was appended only if it was answered.
		select {
		case err := <-appended:
			return err
		default:
			return ErrSinkClosed
		}
	case <-ctx.Done():
		return ctx.Err()
	}
}

// PublishNotice tells live readers about a condition that is not part of the
// Work Message Stream. Readers who are not watching never see it.
func (s *Sink) PublishNotice(ctx context.Context, workID, reason, message string) error {
	return s.diagnostics.PublishDiagnostic(ctx, Diagnostic{AgentWorkID: workID, Severity: "warning", Reason: reason, Message: message, OccurredAt: time.Now().UTC()})
}

// Close appends what is queued and stops the sink.
func (s *Sink) Close(ctx context.Context) error {
	s.closeOnce.Do(func() { close(s.closed) })
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Sink) run() {
	lived := make(chan struct{})
	go func() {
		defer close(lived)
		for msg := range s.published {
			s.publishLive(msg)
		}
	}()
	defer func() { close(s.published); <-lived; close(s.done) }()
	ticker := time.NewTicker(batchInterval)
	defer ticker.Stop()
	var batch []*dispatcherpublicv1.UnifiedMessage
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		err := s.append(batch)
		batch = nil
		return err
	}
	for {
		select {
		case item := <-s.queue:
			batch = append(batch, item.message)
			if item.appended != nil {
				item.appended <- flush()
			} else if len(batch) >= batchSize {
				_ = flush()
			}
			s.handOverLive(item.message)
		case <-ticker.C:
			_ = flush()
		case <-s.closed:
			for {
				select {
				case item := <-s.queue:
					batch = append(batch, item.message)
					if item.appended != nil {
						item.appended <- flush()
					}
					s.handOverLive(item.message)
				default:
					_ = flush()
					return
				}
			}
		}
	}
}

func (s *Sink) append(batch []*dispatcherpublicv1.UnifiedMessage) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.log.Append(ctx, batch...); err != nil {
		s.publishFailure(ctx, batch, failureStorageUnavailable, err)
		return err
	}
	return nil
}

func (s *Sink) handOverLive(msg *dispatcherpublicv1.UnifiedMessage) {
	select {
	case s.published <- msg:
	default:
		s.publishFailure(context.Background(), []*dispatcherpublicv1.UnifiedMessage{msg}, failureLiveUnavailable, errors.New("live publication queue is full"))
	}
}

func (s *Sink) publishLive(msg *dispatcherpublicv1.UnifiedMessage) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.live.PublishLive(ctx, msg); err != nil {
		s.publishFailure(ctx, []*dispatcherpublicv1.UnifiedMessage{msg}, failureLiveUnavailable, err)
	}
}

// publishFailure keeps the cause in operator logs and tells each affected
// Work's live readers only that delivery failed.
func (s *Sink) publishFailure(ctx context.Context, messages []*dispatcherpublicv1.UnifiedMessage, reason string, cause error) {
	told := map[string]bool{}
	for _, msg := range messages {
		work := msg.GetAgentWorkId()
		if told[work] {
			continue
		}
		told[work] = true
		s.logger.WarnContext(ctx, "UnifiedMessage delivery failed", "agent_work_id", work, "messages", len(messages), "failure_kind", reason, "error", cause)
		diagnostic := Diagnostic{AgentWorkID: work, Severity: "error", Reason: reason, Message: "UnifiedMessage delivery failed", OccurredAt: time.Now().UTC()}
		if err := s.diagnostics.PublishDiagnostic(ctx, diagnostic); err != nil {
			s.logger.WarnContext(ctx, "publish delivery diagnostic failed", "agent_work_id", work, "failure_kind", reason, "error", err)
		}
	}
}
