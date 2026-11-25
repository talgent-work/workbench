package nats

import (
	"fmt"
	"io"
	"log/slog"
	"net"
	"testing"

	"github.com/talgent-work/workbench/internal/testsupport/middlewaretest"

	"go.uber.org/fx/fxtest"
)

// A service starts while the broker is unreachable: the first use returns a
// connection the client library keeps trying, not an error.
func TestConnDoesNotWaitForAnUnreachableBroker(t *testing.T) {
	client := NewLazyConn(fxtest.NewLifecycle(t), Config{URL: unusedURL(t)}, discardLogger())
	t.Cleanup(func() { _ = client.Close() })

	nc, err := client.Conn()
	if err != nil {
		t.Fatalf("Conn error = %v, want a reconnecting connection", err)
	}
	if nc.IsConnected() {
		t.Fatal("connection to an unused port reports connected")
	}
	again, err := client.Conn()
	if err != nil || again != nc {
		t.Fatalf("second Conn = %p, %v; want the same reconnecting connection", again, err)
	}
}

// Stopping the process closes the shared connection, and nothing dials again.
func TestCloseOnStopEndsTheConnection(t *testing.T) {
	lc := fxtest.NewLifecycle(t)
	client := NewLazyConn(lc, Config{URL: middlewaretest.NATS(t)}, discardLogger())

	nc, err := client.Conn()
	if err != nil {
		t.Fatalf("Conn error = %v", err)
	}
	lc.RequireStart()
	lc.RequireStop()
	if !nc.IsClosed() {
		t.Fatalf("Status after stop = %v, want closed", nc.Status())
	}
	if _, err := client.Conn(); err == nil {
		t.Fatal("Conn after stop error = nil, want the connection to stay closed")
	}
}

// A lost connection is the client library's to restore, so its subscriptions
// survive; only a connection closed for good is replaced.
func TestConnReplacesOnlyAClosedConnection(t *testing.T) {
	client := NewLazyConn(fxtest.NewLifecycle(t), Config{URL: middlewaretest.NATS(t)}, discardLogger())
	t.Cleanup(func() { _ = client.Close() })

	first, err := client.Conn()
	if err != nil {
		t.Fatalf("first Conn error = %v", err)
	}
	if again, _ := client.Conn(); again != first {
		t.Fatal("Conn replaced an open connection")
	}
	first.Close()

	second, err := client.Conn()
	if err != nil {
		t.Fatalf("second Conn error = %v", err)
	}
	if second == first {
		t.Fatal("Conn returned the closed connection")
	}
	if err := second.Flush(); err != nil {
		t.Fatalf("replacement connection does not reach the broker: %v", err)
	}
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// unusedURL names a local port nothing listens on.
func unusedURL(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a port: %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release the port: %v", err)
	}
	return fmt.Sprintf("nats://%s", address)
}
