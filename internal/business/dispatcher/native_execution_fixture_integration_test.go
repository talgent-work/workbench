//go:build integration

package dispatcher_test

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/stretchr/testify/require"
	dispatcherpublicv1 "github.com/talgent-work/contracts/gen/dispatcher/public/v1"
	"github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/messagedelivery"
	"google.golang.org/protobuf/proto"
)

// nativeCall is the context of a call under the native execution control token.
func nativeCall(ctx context.Context) context.Context {
	ctx, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set("Authorization", "Bearer control-test")
	return ctx
}

type nativeRecords struct {
	rejectPlan, lostPlan  bool
	rejectInputCompletion bool
	notices               []messagedelivery.Diagnostic
	lostInputCompletion   bool
	mu                    sync.Mutex
	observations          []command.ExecutionObservation
	messages              []*dispatcherpublicv1.UnifiedMessage
	rejectExit            bool
	lostExit              bool
	liveUnavailable       bool
	endedConnections      []string
}

func (r *nativeRecords) ReportConnectionEnded(_ context.Context, workID, processID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.endedConnections = append(r.endedConnections, workID+"/"+processID)
	return nil
}

func (r *nativeRecords) connectionsEnded() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.endedConnections...)
}

func (r *nativeRecords) ReportExecution(_ context.Context, o command.ExecutionObservation) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if o.Kind == command.ExecutionWorkProgressUpdated && r.rejectPlan {
		r.lostPlan = true
		return errors.New("injected plan feedback loss")
	}

	if o.Kind == command.ExecutionExited && r.rejectExit {
		r.lostExit = true
		return errors.New("injected exit feedback failure")
	}
	if o.Kind == command.ExecutionInputSettled && r.rejectInputCompletion {
		r.lostInputCompletion = true
		return errors.New("injected input completion feedback loss")
	}
	r.observations = append(r.observations, o)
	return nil
}
func (r *nativeRecords) Append(_ context.Context, messages ...*dispatcherpublicv1.UnifiedMessage) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, m := range messages {
		r.messages = append(r.messages, proto.CloneOf(m))
	}
	return nil
}
func (r *nativeRecords) PublishDiagnostic(_ context.Context, diagnostic messagedelivery.Diagnostic) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.notices = append(r.notices, diagnostic)
	return nil
}
func (r *nativeRecords) PublishLive(context.Context, *dispatcherpublicv1.UnifiedMessage) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.liveUnavailable {
		return errors.New("live publisher unavailable")
	}
	return nil
}

// newTestDelivery delivers into records, publishing live through publisher.
func newTestDelivery(t *testing.T, records *nativeRecords, publisher messagedelivery.LivePublisher) *messagedelivery.Sink {
	t.Helper()
	delivery, err := messagedelivery.NewSink(records, publisher, records, slog.Default())
	require.NoError(t, err)
	t.Cleanup(func() { _ = delivery.Close(context.Background()) })
	return delivery
}

func (r *nativeRecords) awaitWork(t *testing.T, workID string, kind command.ExecutionObservationKind) command.ExecutionObservation {
	t.Helper()
	var found command.ExecutionObservation
	require.Eventually(t, func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		for _, o := range r.observations {
			if o.WorkID == workID && o.Kind == kind {
				found = o
				return true
			}
		}
		return false
	}, 15*time.Second, 10*time.Millisecond, "Work %s missing %s", workID, kind)
	return found
}
