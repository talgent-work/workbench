//go:build integration

package dispatcher_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/dispatcher"
	"github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
	dispatcherredis "github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/redis"
)

// lostNotifications sends a buffer's change notifications to a channel no
// replica listens on, as a notification published while the reader's
// subscription reconnects is lost.
type lostNotifications struct{}

func (lostNotifications) DialHook(next goredis.DialHook) goredis.DialHook { return next }

func (lostNotifications) ProcessHook(next goredis.ProcessHook) goredis.ProcessHook {
	return func(ctx context.Context, cmd goredis.Cmder) error {
		args := cmd.Args()
		for i, arg := range args {
			if arg == dispatcherredis.InputAppendedChannel {
				args[i] = "nobody-listens"
			}
		}
		return next(ctx, cmd)
	}
}

func (lostNotifications) ProcessPipelineHook(next goredis.ProcessPipelineHook) goredis.ProcessPipelineHook {
	return next
}

// unreadableBuffers fails reads of input buffers while failing is set, and
// lets every other command through.
type unreadableBuffers struct{ failing *atomic.Bool }

func (unreadableBuffers) DialHook(next goredis.DialHook) goredis.DialHook { return next }

func (unreadableBuffers) ProcessHook(next goredis.ProcessHook) goredis.ProcessHook { return next }

func (h unreadableBuffers) ProcessPipelineHook(next goredis.ProcessPipelineHook) goredis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []goredis.Cmder) error {
		for _, cmd := range cmds {
			if cmd.Name() != "zrange" || !h.failing.Load() {
				continue
			}
			if key, ok := cmd.Args()[1].(string); ok && strings.HasPrefix(key, "dispatcher:input-order:") {
				return errors.New("input buffer unreadable")
			}
		}
		return next(ctx, cmds)
	}
}

// connectedWork starts reading an idle, ready Harness process.
func connectedWork(t *testing.T, ctx context.Context, owner *dispatcher.NativeExecutions, records *nativeRecords, execd *fakeExecd) command.ConnectExecution {
	t.Helper()
	target := command.ConnectExecution{WorkID: "reconcile-" + uuid.NewString(), Endpoint: execd.server.URL, AccessToken: "fixture", ProcessID: "process", NativeSessionID: uuid.NewString()}
	require.NoError(t, owner.Connect(ctx, target))
	<-execd.attaches
	records.awaitWork(t, target.WorkID, command.ExecutionStarted)
	return target
}

// Intent (real): input buffered for an idle Work whose change notification
// never reaches the reader is written by the reader's next scan.
func TestInputWhoseNotificationIsLostIsWrittenByTheNextScan(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	execd := newFakeExecd(t)
	execd.write(initialized)
	replicas := newNativeReplicas(t)
	records := &nativeRecords{}
	owner := replicas.replica("owner", records, newTestDelivery(t, records, records))
	target := connectedWork(t, ctx, owner, records, execd)

	silent := replicas.client()
	silent.AddHook(lostNotifications{})
	input := command.BufferedInput{ID: uuid.NewString(), Text: "notified nowhere"}
	handOff, err := dispatcherredis.NewInputBuffer(silent).Append(ctx, target.WorkID, input)
	require.NoError(t, err)
	require.Equal(t, command.InputBuffered, handOff)

	scanned, done := context.WithTimeout(ctx, 5*fastTiming.Scan.Duration())
	defer done()
	require.Equal(t, written{Type: "user", UUID: input.ID}, nextWrite(t, scanned, execd))
}

// Intent (real): input whose buffer the reader could not read when notified
// is written once the buffer can be read again, without another notification.
func TestInputIsWrittenOnceItsBufferCanBeReadAgain(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	execd := newFakeExecd(t)
	execd.write(initialized)
	replicas := newNativeReplicas(t)
	records := &nativeRecords{}
	var failing atomic.Bool
	rdb := replicas.client()
	rdb.AddHook(unreadableBuffers{failing: &failing})
	owner, err := dispatcher.NewNativeExecutions(http.DefaultClient, records, newTestDelivery(t, records, records),
		dispatcherredis.NewOwnership(rdb, "owner", "127.0.0.1:0", fastTiming.TTL.Duration(), fastTiming.SnapshotTTL.Duration()),
		dispatcherredis.NewInputBuffer(rdb), fastTiming, slog.Default())
	require.NoError(t, err)
	require.NoError(t, owner.Start(ctx))
	t.Cleanup(func() {
		c, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		_ = owner.Shutdown(c)
	})
	target := connectedWork(t, ctx, owner, records, execd)

	failing.Store(true)
	input := command.BufferedInput{ID: uuid.NewString(), Text: "read later"}
	_, err = replicas.inputs().Append(ctx, target.WorkID, input)
	require.NoError(t, err)
	noWrite(t, execd, 3*fastTiming.Scan.Duration())

	failing.Store(false)
	scanned, done := context.WithTimeout(ctx, 5*fastTiming.Scan.Duration())
	defer done()
	require.Equal(t, written{Type: "user", UUID: input.ID}, nextWrite(t, scanned, execd))
}
