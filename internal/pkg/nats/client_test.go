package nats

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"
	"unsafe"

	"github.com/talgent-work/workbench/internal/testsupport/middlewaretest"

	natsgo "github.com/nats-io/nats.go"
	"go.uber.org/fx/fxtest"
)

// Eager NATS wiring must own the shared connection and release it on fx stop.
func TestNewConnConnectsAndClosesOnStop(t *testing.T) {
	srv := runTestServer(t)
	lc := fxtest.NewLifecycle(t)

	nc, err := NewConn(lc, Config{URL: srv})
	if err != nil {
		t.Fatalf("NewConn error = %v", err)
	}

	lc.RequireStart()
	if got := nc.Status(); got != natsgo.CONNECTED {
		t.Fatalf("Status before stop = %v, want %v", got, natsgo.CONNECTED)
	}

	lc.RequireStop()
	if got := nc.Status(); got != natsgo.CLOSED {
		t.Fatalf("Status after stop = %v, want %v", got, natsgo.CLOSED)
	}
}

// Lazy NATS wiring must not block service startup when the broker is unavailable.
func TestNewLazyConnDefersDialUntilFirstUse(t *testing.T) {
	lc := fxtest.NewLifecycle(t)
	client := NewLazyConn(lc, Config{URL: unusedURL(t)})

	lc.RequireStart()
	if _, err := client.Conn(); err == nil {
		t.Fatal("Conn error = nil, want connection error")
	}
}

// Lazy NATS wiring must still release an opened connection during fx shutdown.
func TestNewLazyConnClosesCurrentConnectionOnStop(t *testing.T) {
	srv := runTestServer(t)
	lc := fxtest.NewLifecycle(t)
	client := NewLazyConn(lc, Config{URL: srv})

	nc, err := client.Conn()
	if err != nil {
		t.Fatalf("Conn error = %v", err)
	}
	if got := nc.Status(); got != natsgo.CONNECTED {
		t.Fatalf("Status before stop = %v, want %v", got, natsgo.CONNECTED)
	}

	lc.RequireStart()
	lc.RequireStop()
	if got := nc.Status(); got != natsgo.CLOSED {
		t.Fatalf("Status after stop = %v, want %v", got, natsgo.CLOSED)
	}
}

// Lazy NATS live publishers must recover after a broker-side stale handle by
// replacing a closed connection on the next publish attempt.
func TestNewLazyConnRedialsAfterClosedConnection(t *testing.T) {
	srv := runTestServer(t)
	lc := fxtest.NewLifecycle(t)
	client := NewLazyConn(lc, Config{URL: srv})

	first, err := client.Conn()
	if err != nil {
		t.Fatalf("first Conn error = %v", err)
	}
	first.Close()
	if got := first.Status(); got != natsgo.CLOSED {
		t.Fatalf("first status = %v, want %v", got, natsgo.CLOSED)
	}

	second, err := client.Conn()
	if err != nil {
		t.Fatalf("second Conn error = %v", err)
	}
	if second == first {
		t.Fatal("second Conn returned the prior closed connection")
	}
	if got := second.Status(); got != natsgo.CONNECTED {
		t.Fatalf("second status = %v, want %v", got, natsgo.CONNECTED)
	}
}

// Lazy NATS live publishers must also recover from a broker-side disconnect by
// replacing the disconnected connection on the next publish attempt.
func TestNewLazyConnRedialsAfterDisconnectedConnection(t *testing.T) {
	srv := runTestServer(t)
	lc := fxtest.NewLifecycle(t)
	client := NewLazyConn(lc, Config{URL: srv})

	first, err := client.Conn()
	if err != nil {
		t.Fatalf("first Conn error = %v", err)
	}
	forceNATSStatus(t, first, natsgo.DISCONNECTED)
	if got := first.Status(); got != natsgo.DISCONNECTED {
		t.Fatalf("first status = %v, want %v", got, natsgo.DISCONNECTED)
	}

	second, err := client.Conn()
	if err != nil {
		t.Fatalf("second Conn error = %v", err)
	}
	if second == first {
		t.Fatal("second Conn returned the prior disconnected connection")
	}
	if got := second.Status(); got != natsgo.CONNECTED {
		t.Fatalf("second status = %v, want %v", got, natsgo.CONNECTED)
	}
}

func runTestServer(t *testing.T) string { return middlewaretest.NATS(t) }

func forceNATSStatus(t *testing.T, nc *natsgo.Conn, status natsgo.Status) {
	t.Helper()

	field := reflect.ValueOf(nc).Elem().FieldByName("status")
	if !field.IsValid() {
		t.Fatal("nats.Conn status field not found")
	}
	reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().Set(reflect.ValueOf(status))
}

func unusedURL(t *testing.T) string {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	for port := 42000; port < 42100; port++ {
		url := fmt.Sprintf("nats://127.0.0.1:%d", port)
		if _, err := natsgo.Connect(url, natsgo.NoReconnect(), natsgo.Timeout(10*time.Millisecond)); errors.Is(err, natsgo.ErrTimeout) || err != nil {
			return url
		}
		select {
		case <-ctx.Done():
			t.Fatal("no unused nats URL found")
		default:
		}
	}
	t.Fatal("no unused nats URL found")
	return ""
}
