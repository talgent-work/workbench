//go:build integration

package dispatcher_test

import (
	"context"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/dispatcher"
	"github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
	dispatcherredis "github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/redis"
)

func attachmentTarget(sandbox *flakySandbox) command.ConnectExecution {
	return command.ConnectExecution{WorkID: uuid.NewString(), ProcessID: "process", NativeSessionID: uuid.NewString(), Endpoint: sandbox.server.URL, AccessToken: "execd"}
}

func awaitGated(t *testing.T, ctx context.Context, sandbox *flakySandbox) {
	t.Helper()
	select {
	case <-sandbox.gated:
	case <-ctx.Done():
		t.Fatal("the attempt to attach never reached the sandbox")
	}
}

// A first attempt that fails is tried once more before Agent hears of the
// loss, which counts from that first failure.
func TestAFirstFailedAttachIsTriedAgainBeforeALossIsReported(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	sandbox := newFlakySandbox(t, refuseAttachment, refuseAttachment, holdAttachment)
	records := &nativeRecords{}
	manager := newNativeReplicas(t).replica("replica", records, newTestDelivery(t, records, records))
	target := attachmentTarget(sandbox)

	before := time.Now()
	require.NoError(t, manager.Connect(ctx, target))
	require.Eventually(t, func() bool { return len(records.connectionsEnded()) == 1 }, 15*time.Second, 20*time.Millisecond)
	failed := time.Now()
	require.Empty(t, records.attachmentReports(), "a first failure is tried again first")

	reconnectAfter(t, ctx, records, 1, manager, target)
	require.Eventually(t, func() bool { return len(records.attachmentReports()) == 1 }, 15*time.Second, 20*time.Millisecond)
	lost := records.attachmentReports()[0]
	require.Equal(t, command.ExecutionAttachmentLost, lost.Kind)
	require.False(t, lost.DetachedSince.Before(before.Truncate(time.Millisecond)) || lost.DetachedSince.After(failed), "the loss counts from the first failure")

	reconnectAfter(t, ctx, records, 2, manager, target)
	awaitAttached(t, ctx, manager, target)
	require.Eventually(t, func() bool { return len(records.attachmentReports()) == 2 }, 15*time.Second, 20*time.Millisecond)
	require.Equal(t, command.ExecutionAttachmentReturned, records.attachmentReports()[1].Kind)
}

// A replica that hands its stream over while it attaches again gives up the
// state, so the peer resumes the loss with its origin and reports it on its
// own failed attempt.
func TestAHandoverKeepsTheLossItsReplicaWasRetrying(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	sandbox := newFlakySandbox(t, dropAttachment, gateAttachment, refuseAttachment)
	records := &nativeRecords{}
	replicas := newNativeReplicas(t)
	first := replicas.replica("first", records, newTestDelivery(t, records, records))
	replicas.replica("second", records, newTestDelivery(t, records, records))
	target := attachmentTarget(sandbox)

	before := time.Now()
	require.NoError(t, first.Connect(ctx, target))
	reconnectAfter(t, ctx, records, 1, first, target)
	ended := time.Now()
	awaitGated(t, ctx, sandbox)
	shutdown, done := context.WithTimeout(ctx, 5*time.Second)
	defer done()
	require.NoError(t, first.Shutdown(shutdown))

	require.Eventually(t, func() bool { return len(records.attachmentReports()) == 1 }, 15*time.Second, 20*time.Millisecond)
	lost := records.attachmentReports()[0]
	require.Equal(t, command.ExecutionAttachmentLost, lost.Kind)
	require.False(t, lost.DetachedSince.Before(before.Truncate(time.Millisecond)) || lost.DetachedSince.After(ended), "the peer keeps the origin of the attachment's end")
}

// A replica that attached again but could not record it, and then vanished,
// leaves a state it resumed: the replica that takes the stream over does not
// resume that stale loss, so it never reports an origin older than the
// regained attachment.
func TestATakeoverDoesNotResumeALossItsVanishedReaderMayHaveEnded(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	sandbox := newFlakySandbox(t, dropAttachment, gateAttachment, refuseAttachment)
	records := &nativeRecords{}
	replicas := newNativeReplicas(t)
	delivery := newTestDelivery(t, records, records)
	vanishing := replicas.client()
	first, err := dispatcher.NewNativeExecutions(http.DefaultClient, records, delivery, dispatcherredis.NewOwnership(vanishing, "first", "127.0.0.1:0", fastTiming.TTL.Duration(), fastTiming.SnapshotTTL.Duration()), dispatcherredis.NewInputBuffer(vanishing), fastTiming, slog.Default())
	require.NoError(t, err)
	require.NoError(t, first.Start(ctx))
	second := replicas.replica("second", records, delivery)
	target := attachmentTarget(sandbox)

	require.NoError(t, first.Connect(ctx, target))
	reconnectAfter(t, ctx, records, 1, first, target)
	awaitGated(t, ctx, sandbox)
	// The first replica can no longer write: the regained attachment it is
	// about to clear the loss on is never recorded.
	require.NoError(t, vanishing.Close())
	regained := time.Now()
	close(sandbox.release)
	awaitAttached(t, ctx, first, target)

	// The second replica takes the stream over once the first one's liveness
	// expires, fails to attach and tries once more before reporting.
	require.Eventually(t, func() bool { return len(records.connectionsEnded()) == 2 }, 20*time.Second, 20*time.Millisecond)
	require.Empty(t, records.attachmentReports(), "the takeover's first failure is tried again first")
	reconnectAfter(t, ctx, records, 2, second, target)
	require.Eventually(t, func() bool { return len(records.attachmentReports()) == 1 }, 15*time.Second, 20*time.Millisecond)
	lost := records.attachmentReports()[0]
	require.Equal(t, command.ExecutionAttachmentLost, lost.Kind)
	require.True(t, lost.DetachedSince.After(regained), "the loss counts from the takeover's failure, not the stale origin")
}
