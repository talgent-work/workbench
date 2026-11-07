package computergateway

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/alicebob/miniredis/v2"
	"github.com/coder/websocket"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	computerprivatev1 "github.com/talgent-work/contracts/gen/go/computer/private/v1"
	"github.com/talgent-work/contracts/gen/go/computer/private/v1/computerprivatev1connect"
	"github.com/talgent-work/workbench/internal/pkg/computerconnection"
)

// computer stands in for Computer's connection contract.
type computer struct {
	computerprivatev1connect.UnimplementedConnectionServiceHandler
	mu      sync.Mutex
	reports []string
	// sandboxes are the sandbox reports of each reachable report.
	sandboxes []bool
	// minimum is the Minimum Version Computer answers; 0.2.0 when empty.
	minimum string
	// unreconciling answers as a platform that does not reconcile machines.
	unreconciling bool
	// removed makes the good credential the one of a removed Computer,
	// revoked one Computer rejects, and callerRefused has Computer refuse
	// the Gateway's control token.
	removed, revoked, callerRefused bool
}

func (c *computer) Authenticate(_ context.Context, req *computerprivatev1.AuthenticateRequest) (*computerprivatev1.AuthenticateResponse, error) {
	c.mu.Lock()
	removed, revoked, callerRefused := c.removed, c.revoked, c.callerRefused
	c.mu.Unlock()
	refused := func(refusal computerprivatev1.CredentialRefusal) (*computerprivatev1.AuthenticateResponse, error) {
		return &computerprivatev1.AuthenticateResponse{Refusal: refusal}, nil
	}
	if callerRefused {
		return nil, connect.NewError(connect.CodeUnauthenticated, "control authentication required")
	}
	switch req.GetCredential() {
	case "good", "next":
		if removed {
			return refused(computerprivatev1.CredentialRefusal_CREDENTIAL_REFUSAL_COMPUTER_REMOVED)
		}
		if revoked {
			return refused(computerprivatev1.CredentialRefusal_CREDENTIAL_REFUSAL_REJECTED)
		}
		return &computerprivatev1.AuthenticateResponse{ComputerId: "c1"}, nil
	case "due":
		return &computerprivatev1.AuthenticateResponse{ComputerId: "c1", RotationDue: true}, nil
	case "removed":
		return refused(computerprivatev1.CredentialRefusal_CREDENTIAL_REFUSAL_COMPUTER_REMOVED)
	case "outage":
		return nil, connect.NewError(connect.CodeUnavailable, "down")
	default:
		return refused(computerprivatev1.CredentialRefusal_CREDENTIAL_REFUSAL_REJECTED)
	}
}

func (c *computer) ReportReachable(_ context.Context, req *computerprivatev1.ReportReachableRequest) (*computerprivatev1.ReportReachableResponse, error) {
	c.record("reachable " + req.GetComputerId() + " " + req.GetVersions().GetTalgentComputer())
	c.mu.Lock()
	c.sandboxes = append(c.sandboxes, req.GetClaudeCodeSandbox())
	minimum, reconciles := c.minimum, !c.unreconciling
	c.mu.Unlock()
	if minimum == "" {
		minimum = "0.2.0"
	}
	return &computerprivatev1.ReportReachableResponse{MinimumVersion: minimum, Reconciles: reconciles}, nil
}

func (c *computer) ReportUnreachable(_ context.Context, req *computerprivatev1.ReportUnreachableRequest) (*computerprivatev1.ReportUnreachableResponse, error) {
	c.record("unreachable " + req.GetComputerId())
	return &computerprivatev1.ReportUnreachableResponse{}, nil
}

func (c *computer) RotateCredential(_ context.Context, req *computerprivatev1.RotateCredentialRequest) (*computerprivatev1.RotateCredentialResponse, error) {
	c.record("rotate " + req.GetComputerId() + " " + req.GetCredential())
	return &computerprivatev1.RotateCredentialResponse{Credential: "next", ExpiresAtUnixMs: 1_900_000_000_000}, nil
}

func (c *computer) ConfirmCredential(_ context.Context, req *computerprivatev1.ConfirmCredentialRequest) (*computerprivatev1.ConfirmCredentialResponse, error) {
	c.record("confirm " + req.GetComputerId() + " " + req.GetCredential())
	return &computerprivatev1.ConfirmCredentialResponse{}, nil
}

func (c *computer) record(report string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reports = append(c.reports, report)
}

func (c *computer) seen() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.reports...)
}

type fixture struct {
	gateway  *Gateway
	computer *computer
	reader   *computerconnection.Reader
	writer   *computerconnection.Writer
	url      string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	return newFixtureLogging(t, slog.Default())
}

func newFixtureLogging(t *testing.T, logger *slog.Logger) *fixture {
	t.Helper()
	stand := &computer{}
	mux := http.NewServeMux()
	srv := connect.NewServer()
	computerprivatev1connect.RegisterConnectionServiceHandler(srv, stand)
	connecthttp.Mount(mux, srv, connecthttp.WithReadMaxBytes(0))
	backend := httptest.NewServer(mux)
	t.Cleanup(backend.Close)
	rdb := goredis.NewClient(&goredis.Options{Addr: miniredis.RunT(t).Addr()})
	gateway := NewGateway(computerprivatev1connect.NewConnectionServiceClient(connect.NewClient(connecthttp.NewTransport(http.DefaultClient, backend.URL, connecthttp.WithReadMaxBytes(0)))), computerconnection.NewWriter(rdb), computerconnection.NewReader(rdb), Replica{Name: "replica-a", Endpoint: "192.0.2.1:8085", ControlToken: "control", TargetVersion: "0.3.0"}, logger)
	front := http.NewServeMux()
	front.HandleFunc("GET /computer/v1/connect", gateway.ServeConnect)
	server := httptest.NewServer(front)
	t.Cleanup(server.Close)
	return &fixture{gateway: gateway, computer: stand, reader: computerconnection.NewReader(rdb), writer: computerconnection.NewWriter(rdb), url: "ws" + strings.TrimPrefix(server.URL, "http") + "/computer/v1/connect"}
}

func (f *fixture) dial(t *testing.T, credential string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	return f.dialWith(t, http.Header{"Authorization": {"Bearer " + credential}, HeaderTalgentComputerVersion: {"0.1.0"}, HeaderCredentialRotation: {"supported"}})
}

func (f *fixture) dialWith(t *testing.T, header http.Header) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return websocket.Dial(ctx, f.url, &websocket.DialOptions{HTTPHeader: header})
}

// A machine reports the sandbox only by saying it is available; an older
// Talgent Computer that says nothing cannot provide it.
func TestAConnectionReportsWhetherItsMachineProvidesTheSandbox(t *testing.T) {
	for _, value := range []string{"available", "", "unavailable"} {
		f := newFixture(t)
		header := http.Header{"Authorization": {"Bearer good"}, HeaderTalgentComputerVersion: {"0.1.0"}}
		if value != "" {
			header.Set(HeaderClaudeCodeSandbox, value)
		}
		conn, _, err := f.dialWith(t, header)
		require.NoError(t, err)
		require.Equal(t, "welcome", read(t, conn).Type)
		f.computer.mu.Lock()
		require.Equal(t, []bool{value == "available"}, f.computer.sandboxes, value)
		f.computer.mu.Unlock()
		require.NoError(t, conn.Close(websocket.StatusNormalClosure, ""))
	}
}

func read(t *testing.T, conn *websocket.Conn) message {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, data, err := conn.Read(ctx)
	require.NoError(t, err)
	var m message
	require.NoError(t, json.Unmarshal(data, &m))
	return m
}

func TestTheGatewayRefusesACredentialComputerRejects(t *testing.T) {
	f := newFixture(t)
	_, response, err := f.dial(t, "bad")
	require.Error(t, err)
	require.Equal(t, http.StatusUnauthorized, response.StatusCode)
	_, response, err = f.dial(t, "removed")
	require.Error(t, err)
	require.Equal(t, http.StatusForbidden, response.StatusCode)
}

// Computer refusing the Gateway itself, as a wrong control token, is no
// refusal of the machine's credential: the machine is told to try again.
func TestACallerFaultIsNoCredentialRefusal(t *testing.T) {
	f := newFixture(t)
	f.computer.mu.Lock()
	f.computer.callerRefused = true
	f.computer.mu.Unlock()
	_, response, err := f.dial(t, "good")
	require.Error(t, err)
	require.Equal(t, http.StatusServiceUnavailable, response.StatusCode)
}

// The welcome tells the machine the platform reconciles it only as Computer
// answers, so a machine never waits for a platform that does not.
func TestTheWelcomeAnnouncesReconciliationAsComputerAnswers(t *testing.T) {
	f := newFixture(t)
	f.computer.mu.Lock()
	f.computer.unreconciling = true
	f.computer.mu.Unlock()
	conn, _, err := f.dial(t, "good")
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, data, err := conn.Read(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"type":"welcome","computer_id":"c1","heartbeat_interval_ms":30000,"target_version":"0.3.0","minimum_version":"0.2.0","reconciles":false}`, string(data))
}

func TestAConnectionKeepsItsComputerReachableUntilItEnds(t *testing.T) {
	f := newFixture(t)
	conn, _, err := f.dial(t, "good")
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, data, err := conn.Read(ctx)
	require.NoError(t, err)
	// The welcome announces the Target Version this deployment runs and the
	// Minimum Version Computer admits runs under.
	require.JSONEq(t, `{"type":"welcome","computer_id":"c1","heartbeat_interval_ms":30000,"target_version":"0.3.0","minimum_version":"0.2.0","reconciles":true}`, string(data))
	record, ok, err := f.reader.Lookup(context.Background(), "c1")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "replica-a", record.Replica)
	require.Equal(t, []string{"reachable c1 0.1.0"}, f.computer.seen())

	require.NoError(t, conn.Write(context.Background(), websocket.MessageText, []byte(`{"type":"heartbeat"}`)))
	require.Equal(t, "heartbeat_ack", read(t, conn).Type)

	require.NoError(t, conn.Close(websocket.StatusNormalClosure, "bye"))
	require.Eventually(t, func() bool { return len(f.computer.seen()) == 2 }, 5*time.Second, 20*time.Millisecond)
	require.Equal(t, "unreachable c1", f.computer.seen()[1])
	_, ok, err = f.reader.Lookup(context.Background(), "c1")
	require.NoError(t, err)
	require.False(t, ok)
}

func TestANewerConnectionReplacesTheOlder(t *testing.T) {
	f := newFixture(t)
	older, _, err := f.dial(t, "good")
	require.NoError(t, err)
	read(t, older)
	newer, _, err := f.dial(t, "good")
	require.NoError(t, err)
	read(t, newer)

	require.NoError(t, older.Write(context.Background(), websocket.MessageText, []byte(`{"type":"heartbeat"}`)))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, err = older.Read(ctx)
	require.Equal(t, CloseReplaced, websocket.CloseStatus(err))
	_, ok, err := f.reader.Lookup(context.Background(), "c1")
	require.NoError(t, err)
	require.True(t, ok, "the older connection leaves the newer one's record")
	require.NotContains(t, f.computer.seen(), "unreachable c1")
	_ = newer.Close(websocket.StatusNormalClosure, "")
}

func TestTheGatewayAsksAMachineToRetryWhileComputerIsDown(t *testing.T) {
	f := newFixture(t)
	_, response, err := f.dial(t, "outage")
	require.Error(t, err)
	require.Equal(t, http.StatusServiceUnavailable, response.StatusCode)
}

func TestAMachineReportsItsVersion(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, response, err := websocket.Dial(ctx, f.url, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer good"}}})
	require.Error(t, err)
	require.Equal(t, http.StatusBadRequest, response.StatusCode)
	require.Empty(t, f.computer.seen())
}

// Computer's Minimum Version above this service's Target Version is a
// misconfiguration: it is logged once, and the welcome still carries both.
func TestAMinimumAboveTheTargetIsLoggedOnceAndAnnouncedAsConfigured(t *testing.T) {
	logs := &syncBuffer{}
	f := newFixtureLogging(t, slog.New(slog.NewTextHandler(logs, nil)))
	f.computer.mu.Lock()
	f.computer.minimum = "0.4.0"
	f.computer.mu.Unlock()
	for range 2 {
		conn, _, err := f.dial(t, "good")
		require.NoError(t, err)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, data, err := conn.Read(ctx)
		cancel()
		require.NoError(t, err)
		require.JSONEq(t, `{"type":"welcome","computer_id":"c1","heartbeat_interval_ms":30000,"target_version":"0.3.0","minimum_version":"0.4.0","reconciles":true}`, string(data))
		require.NoError(t, conn.Close(websocket.StatusNormalClosure, "bye"))
	}
	require.Equal(t, 1, strings.Count(logs.String(), "the Minimum Version is above the Target Version"), logs.String())
	require.Contains(t, logs.String(), "level=ERROR")
}

// syncBuffer is a log sink the gateway's goroutines write while a test reads.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestASilentConnectionIsEnded(t *testing.T) {
	f := newFixture(t)
	f.gateway.silence = 100 * time.Millisecond
	conn, _, err := f.dial(t, "good")
	require.NoError(t, err)
	read(t, conn)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, err = conn.Read(ctx)
	require.Equal(t, CloseSilent, websocket.CloseStatus(err))
	require.Eventually(t, func() bool { return len(f.computer.seen()) == 2 }, 5*time.Second, 20*time.Millisecond)
}

func TestAStoppingGatewaySendsItsMachinesElsewhere(t *testing.T) {
	f := newFixture(t)
	conn, _, err := f.dial(t, "good")
	require.NoError(t, err)
	read(t, conn)
	closed := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _, err := conn.Read(ctx)
		closed <- err
	}()
	f.gateway.Stop()
	require.Equal(t, websocket.StatusServiceRestart, websocket.CloseStatus(<-closed))
	require.Eventually(t, func() bool {
		_, ok, _ := f.reader.Lookup(context.Background(), "c1")
		return !ok
	}, 5*time.Second, 20*time.Millisecond)
	require.NotContains(t, f.computer.seen(), "unreachable c1", "the machine reconnects elsewhere; the sweep settles a lost one")
	_, response, err := f.dial(t, "good")
	require.Error(t, err)
	require.Equal(t, http.StatusServiceUnavailable, response.StatusCode, "a stopping replica takes no new connection")
}
