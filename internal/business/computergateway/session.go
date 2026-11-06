package computergateway

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/coder/websocket"
	"github.com/google/uuid"

	computerprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/computer/private/v1"
	"github.com/talgent-work/workbench/gen/go/talgent/computer/private/v1/computerprivatev1connect"
	"github.com/talgent-work/workbench/internal/pkg/computerconnection"
)

const (
	// HeartbeatInterval is how often Talgent Computer sends a heartbeat.
	HeartbeatInterval = 30 * time.Second
	// Silence ends a connection that sent nothing for three heartbeats.
	Silence = 3 * HeartbeatInterval
	// callTimeout bounds each call to Computer.
	callTimeout = 10 * time.Second
	// readLimit bounds a frame from a pipe's client.
	readLimit = 64 << 10
	// machineReadLimit bounds a frame from the machine: a stream id and a
	// pipe frame or a file read's piece, each under 256 KiB.
	machineReadLimit = 256 << 10
	// maxVersionLength is the longest version Computer records.
	maxVersionLength = 64

	// Close codes Talgent Computer acts on.
	CloseCredentialRejected websocket.StatusCode = 4401
	CloseComputerRemoved    websocket.StatusCode = 4403
	CloseSilent             websocket.StatusCode = 4408
	CloseReplaced           websocket.StatusCode = 4409

	HeaderTalgentComputerVersion = "X-Talgent-Computer-Version"
	HeaderClaudeCodeVersion      = "X-Talgent-Claude-Code-Version"
	// HeaderClaudeCodeSandbox is "available" when the machine can provide
	// Claude Code's sandbox; any other value or none means it cannot.
	HeaderClaudeCodeSandbox = "X-Talgent-Claude-Code-Sandbox"
)

// message is one JSON text frame of the connection protocol.
type message struct {
	Type                string `json:"type"`
	ComputerID          string `json:"computer_id,omitempty"`
	HeartbeatIntervalMS int64  `json:"heartbeat_interval_ms,omitempty"`
}

// welcome is the connection's first frame: who the machine is, how often it
// sends a heartbeat, and the versions the platform announces, each "" when
// there is none.
type welcome struct {
	Type                string `json:"type"`
	ComputerID          string `json:"computer_id"`
	HeartbeatIntervalMS int64  `json:"heartbeat_interval_ms"`
	// TargetVersion is the release every Managed Computer should run.
	TargetVersion string `json:"target_version"`
	// MinimumVersion is the oldest release that may take a run.
	MinimumVersion string `json:"minimum_version"`
	// Reconciles tells the machine that the platform reconciles it on this
	// connection, so it holds back its starts until then. It follows
	// Computer's answer, so a platform that does not reconcile is never
	// waited for.
	Reconciles bool `json:"reconciles"`
}

// Gateway holds the outbound connections of Managed Computers, one per
// Computer, records which replica holds each, and reports their reach to
// Computer.
type Gateway struct {
	computers   computerprivatev1connect.ConnectionServiceClient
	records     *computerconnection.Writer
	connections connections
	replica     string
	endpoint    string
	// controlToken admits Agent to open a Work's environment.
	controlToken string
	// target is the Target Version each connection is told.
	target  string
	logger  *slog.Logger
	now     func() time.Time
	silence time.Duration
	// revalidate is how often each connection's credential is checked again.
	revalidate time.Duration
	// successorWait bounds the wait for a machine's answer to its rotated
	// credential.
	successorWait time.Duration
	// fileIdle bounds the wait for each piece of a relayed file read.
	fileIdle time.Duration

	mu       sync.Mutex
	stopping bool
	// misorderedMinimum is the last Minimum Version above the Target this
	// replica logged, so each misconfiguration is logged once.
	misorderedMinimum string
	open              map[*websocket.Conn]struct{}
	tunnels           map[string]*tunnel
}

// Replica is where this replica runs and what it relays with.
type Replica struct {
	// Name and Endpoint identify this replica in connection records.
	Name, Endpoint string
	// ControlToken admits Agent to open a Work's environment.
	ControlToken string
	// TargetVersion is the platform release every machine is told to run;
	// empty for a deployment that is no release.
	TargetVersion string
}

func NewGateway(computers computerprivatev1connect.ConnectionServiceClient, records *computerconnection.Writer, reader connections, replica Replica, logger *slog.Logger) *Gateway {
	return &Gateway{
		computers: computers, records: records, connections: reader, replica: replica.Name, endpoint: replica.Endpoint,
		controlToken: replica.ControlToken, target: replica.TargetVersion, logger: logger, now: time.Now, silence: Silence, revalidate: Revalidate, successorWait: SuccessorWait, fileIdle: FileIdle,
		open: map[*websocket.Conn]struct{}{}, tunnels: map[string]*tunnel{},
	}
}

// ServeConnect authenticates a machine by its Computer Credential and holds
// its connection until either side ends it.
func (g *Gateway) ServeConnect(w http.ResponseWriter, r *http.Request) {
	credential, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || strings.TrimSpace(credential) == "" {
		http.Error(w, "computer credential required", http.StatusUnauthorized)
		return
	}
	versions := &computerprivatev1.ReportedVersions{TalgentComputer: strings.TrimSpace(r.Header.Get(HeaderTalgentComputerVersion)), ClaudeCode: strings.TrimSpace(r.Header.Get(HeaderClaudeCodeVersion))}
	report := machineReport{versions: versions, sandbox: strings.TrimSpace(r.Header.Get(HeaderClaudeCodeSandbox)) == "available"}
	if versions.TalgentComputer == "" || len(versions.TalgentComputer) > maxVersionLength || len(versions.ClaudeCode) > maxVersionLength {
		http.Error(w, "the Talgent Computer version header is required and versions are at most 64 bytes", http.StatusBadRequest)
		return
	}
	if g.isStopping() {
		http.Error(w, "gateway stopping", http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), callTimeout)
	authenticated, err := g.computers.Authenticate(ctx, connect.NewRequest(&computerprivatev1.AuthenticateRequest{Credential: credential}))
	cancel()
	// Only a refusal Computer answers turns the machine away for good; an
	// error leaves the credential unchecked, and the machine retries.
	if err != nil {
		g.logger.WarnContext(r.Context(), "computer gateway: authentication unavailable", "error", err)
		http.Error(w, "computer unavailable", http.StatusServiceUnavailable)
		return
	}
	switch authenticated.Msg.GetRefusal() {
	case computerprivatev1.CredentialRefusal_CREDENTIAL_REFUSAL_UNSPECIFIED:
	case computerprivatev1.CredentialRefusal_CREDENTIAL_REFUSAL_COMPUTER_REMOVED:
		http.Error(w, "computer removed", http.StatusForbidden)
		return
	case computerprivatev1.CredentialRefusal_CREDENTIAL_REFUSAL_REJECTED:
		http.Error(w, "computer credential rejected", http.StatusUnauthorized)
		return
	default:
		g.logger.WarnContext(r.Context(), "computer gateway: authentication unavailable", "refusal", authenticated.Msg.GetRefusal().String())
		http.Error(w, "computer unavailable", http.StatusServiceUnavailable)
		return
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	if !g.track(conn) {
		_ = conn.Close(websocket.StatusServiceRestart, "gateway stopping")
		return
	}
	defer g.untrack(conn)
	conn.SetReadLimit(machineReadLimit)
	g.hold(context.WithoutCancel(r.Context()), conn, machine{
		computerID: authenticated.Msg.GetComputerId(), credential: credential, rotationDue: authenticated.Msg.GetRotationDue(),
		rotates: strings.TrimSpace(r.Header.Get(HeaderCredentialRotation)) == "supported", report: report,
	})
}

// machine is the authenticated machine of one connection: its Computer, the
// credential it presented, whether that credential is due for rotation,
// whether the machine supports rotation, and what it reports.
type machine struct {
	computerID  string
	credential  string
	rotationDue bool
	rotates     bool
	report      machineReport
}

// machineReport is what a machine reports on connecting: its versions and
// whether it can provide Claude Code's sandbox.
type machineReport struct {
	versions *computerprivatev1.ReportedVersions
	sandbox  bool
}

func (g *Gateway) hold(ctx context.Context, conn *websocket.Conn, m machine) {
	computerID, report := m.computerID, m.report
	logger := g.logger.With("computer_id", computerID)
	connection := computerconnection.Connection{ID: uuid.NewString(), Replica: g.replica, Endpoint: g.endpoint}
	if err := g.records.Claim(ctx, computerID, connection); err != nil {
		logger.WarnContext(ctx, "computer gateway: connection not recorded", "error", err)
		_ = conn.Close(websocket.StatusTryAgainLater, "try again")
		return
	}
	defer g.release(ctx, computerID, connection.ID, logger)
	told, err := g.reportReachable(ctx, computerID, report)
	if err != nil {
		logger.WarnContext(ctx, "computer gateway: reach not reported", "error", err)
		_ = conn.Close(websocket.StatusTryAgainLater, "try again")
		return
	}
	logger.InfoContext(ctx, "computer gateway: connected", "connection_id", connection.ID)
	g.checkMinimum(ctx, told.minimum)
	relay := newTunnel(conn)
	// The machine learns its heartbeat interval and versions before any
	// relayed call.
	if err := relay.sendJSON(ctx, welcome{Type: "welcome", ComputerID: computerID, HeartbeatIntervalMS: HeartbeatInterval.Milliseconds(), TargetVersion: g.target, MinimumVersion: told.minimum, Reconciles: told.reconciles}); err != nil {
		return
	}
	g.holdTunnel(computerID, relay)
	defer g.dropTunnel(computerID, relay)
	// The credential is checked again, and rotated, while the connection
	// lasts.
	watching, endWatch := context.WithCancel(ctx)
	defer endWatch()
	credentials := newCredentialWatch(g, conn, relay, m, logger)
	go credentials.run(watching, m.rotationDue && m.rotates)
	// A cancelled read would drop the connection without a close code, so
	// silence is timed apart from reading.
	silent := time.AfterFunc(g.silence, func() { _ = conn.Close(CloseSilent, "no heartbeat") })
	defer silent.Stop()
	for {
		kind, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		silent.Reset(g.silence)
		if relay.deliver(ctx, kind, data) {
			continue
		}
		if kind == websocket.MessageText && credentials.answer(credentialAnswer(data)) {
			continue
		}
		var received message
		if kind != websocket.MessageText || json.Unmarshal(data, &received) != nil || received.Type != "heartbeat" {
			continue
		}
		refresh, err := g.records.Refresh(ctx, computerID, connection)
		if err != nil {
			logger.WarnContext(ctx, "computer gateway: connection not refreshed", "error", err)
		}
		switch refresh {
		case computerconnection.Replaced:
			_ = conn.Close(CloseReplaced, "replaced by a newer connection")
			return
		case computerconnection.Reclaimed:
			if _, err := g.reportReachable(ctx, computerID, report); err != nil {
				logger.WarnContext(ctx, "computer gateway: reach not reported", "error", err)
			}
		}
		if err := relay.sendJSON(ctx, message{Type: "heartbeat_ack"}); err != nil {
			return
		}
	}
}

// checkMinimum logs, once per value, a Minimum Version above the Target
// Version. The welcome still carries both as configured: machines upgrade
// to the Target and, below the Minimum, take no new run until either
// configuration is corrected.
func (g *Gateway) checkMinimum(ctx context.Context, minimum string) {
	if !minimumAboveTarget(g.target, minimum) {
		return
	}
	g.mu.Lock()
	first := g.misorderedMinimum != minimum
	g.misorderedMinimum = minimum
	g.mu.Unlock()
	if first {
		g.logger.ErrorContext(ctx, "computer gateway: the Minimum Version is above the Target Version; every Managed Computer takes no new run until computer.minimum-version is lowered to the Target or a later release is deployed",
			"target_version", g.target, "minimum_version", minimum)
	}
}

// machineTold is what Computer has the machine told on connecting: the
// Minimum Version it admits runs under, and whether the platform reconciles
// the machine.
type machineTold struct {
	minimum    string
	reconciles bool
}

// reportReachable reports the machine reachable and answers what the
// machine is told.
func (g *Gateway) reportReachable(ctx context.Context, computerID string, report machineReport) (machineTold, error) {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	response, err := g.computers.ReportReachable(ctx, connect.NewRequest(&computerprivatev1.ReportReachableRequest{ComputerId: computerID, Versions: report.versions, ClaudeCodeSandbox: report.sandbox}))
	if err != nil {
		return machineTold{}, err
	}
	return machineTold{minimum: response.Msg.GetMinimumVersion(), reconciles: response.Msg.GetReconciles()}, nil
}

// release forgets the connection's record while it still holds it and
// reports the loss of reach; a replica that is stopping leaves the report
// to the sweep, since its machines reconnect to another replica at once.
func (g *Gateway) release(ctx context.Context, computerID, connectionID string, logger *slog.Logger) {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	released, err := g.records.Release(ctx, computerID, connectionID)
	if err != nil {
		logger.WarnContext(ctx, "computer gateway: connection record left to expire", "error", err)
		return
	}
	logger.InfoContext(ctx, "computer gateway: disconnected", "connection_id", connectionID)
	if !released || g.isStopping() {
		return
	}
	if _, err := g.computers.ReportUnreachable(ctx, connect.NewRequest(&computerprivatev1.ReportUnreachableRequest{ComputerId: computerID, SinceUnixMs: g.now().UnixMilli()})); err != nil {
		logger.WarnContext(ctx, "computer gateway: loss of reach left to the sweep", "error", err)
	}
}

func (g *Gateway) track(conn *websocket.Conn) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.stopping {
		return false
	}
	g.open[conn] = struct{}{}
	return true
}

func (g *Gateway) untrack(conn *websocket.Conn) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.open, conn)
}

func (g *Gateway) isStopping() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.stopping
}

// Stop ends every connection so its machine reconnects to another replica.
func (g *Gateway) Stop() {
	g.mu.Lock()
	g.stopping = true
	open := make([]*websocket.Conn, 0, len(g.open))
	for conn := range g.open {
		open = append(open, conn)
	}
	g.mu.Unlock()
	var wg sync.WaitGroup
	for _, conn := range open {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = conn.Close(websocket.StatusServiceRestart, "gateway restarting")
		}()
	}
	wg.Wait()
}
