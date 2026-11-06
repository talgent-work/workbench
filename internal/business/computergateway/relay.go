package computergateway

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/talgent-work/workbench/internal/pkg/computerconnection"
)

const (
	// HeaderControlToken carries the Computer control token on the calls that
	// open a Work's environment and deliver or forget its credentials.
	HeaderControlToken = "X-Talgent-Control-Token"
	execdTokenHeader   = "X-EXECD-ACCESS-TOKEN"
	// relayedHeader marks a call one replica forwarded to another, so it is
	// never forwarded again.
	relayedHeader   = "X-Talgent-Relayed"
	maxRequestBytes = 1 << 20
	// fileContentPath reads one file of the Work root as a stream.
	fileContentPath = "/files/content"
)

// FileIdle bounds how long a relayed file read waits for the machine's next
// piece, and for the reader to take the last one: a stalled machine or reader
// never holds the response open, and the response is aborted so a truncated
// file is never taken for a whole one.
const FileIdle = 30 * time.Second

// connections reads which replica holds a Computer's connection.
type connections interface {
	Lookup(ctx context.Context, computerID string) (computerconnection.Connection, bool, error)
}

// ServeRelay serves the execd API of one Work on a Managed Computer: through
// this replica's tunnel when it holds the Computer's connection, otherwise
// forwarded to the replica that does.
func (g *Gateway) ServeRelay(w http.ResponseWriter, r *http.Request) {
	computerID, workID, path := r.PathValue("computer"), r.PathValue("work"), "/"+r.PathValue("rest")
	if computerID == "" || workID == "" {
		http.NotFound(w, r)
		return
	}
	g.relay(w, r, computerID, workID, path)
}

// reconciliationPath is the machine's reconciliation, which names no Work.
const reconciliationPath = "/reconciliation"

// ServeMachineRelay serves the machine's own reconciliation on reconnecting,
// relayed as a call that names no Work.
func (g *Gateway) ServeMachineRelay(w http.ResponseWriter, r *http.Request) {
	computerID := r.PathValue("computer")
	if computerID == "" {
		http.NotFound(w, r)
		return
	}
	g.relay(w, r, computerID, "", reconciliationPath)
}

func (g *Gateway) relay(w http.ResponseWriter, r *http.Request, computerID, workID, path string) {
	if t := g.tunnelOf(computerID); t != nil {
		g.serveLocal(w, r, t, workID, path)
		return
	}
	if r.Header.Get(relayedHeader) != "" {
		http.Error(w, "computer not connected", http.StatusServiceUnavailable)
		return
	}
	connection, ok, err := g.connections.Lookup(r.Context(), computerID)
	if err != nil || !ok || connection.Replica == g.replica || connection.Endpoint == "" {
		http.Error(w, "computer not connected", http.StatusServiceUnavailable)
		return
	}
	target := &url.URL{Scheme: "http", Host: connection.Endpoint}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(out *httputil.ProxyRequest) {
			out.SetURL(target)
			out.Out.URL.Path, out.Out.URL.RawPath = r.URL.Path, r.URL.RawPath
			out.Out.Header.Set(relayedHeader, g.replica)
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			g.logger.WarnContext(r.Context(), "computer gateway: relay forward failed", "computer_id", computerID, "error", err)
			http.Error(w, "computer not connected", http.StatusServiceUnavailable)
		},
	}
	proxy.ServeHTTP(w, r)
}

func (g *Gateway) serveLocal(w http.ResponseWriter, r *http.Request, t *tunnel, workID, path string) {
	token := r.Header.Get(execdTokenHeader)
	if workID == "" && !controlled(workID, r.Method, path) {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if controlled(workID, r.Method, path) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get(HeaderControlToken)), []byte(g.controlToken)) != 1 {
			http.Error(w, "control token required", http.StatusUnauthorized)
			return
		}
		token = ""
	}
	if process, ok := pipePath(path); ok && r.Method == http.MethodGet {
		since, err := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
		if err != nil && r.URL.Query().Get("since") != "" || since < 0 {
			http.Error(w, "invalid offset", http.StatusBadRequest)
			return
		}
		g.servePipe(w, r, t, relayFrame{WorkID: workID, ProcessID: process, Since: since, Token: token})
		return
	}
	if path == fileContentPath && r.Method == http.MethodGet {
		g.serveFile(w, r, t, relayFrame{WorkID: workID, Kind: "file", Path: r.URL.Query().Get("path"), Token: token})
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBytes+1))
	if err != nil || len(body) > maxRequestBytes || len(body) > 0 && !json.Valid(body) {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), callTimeout)
	defer cancel()
	// The query travels with the path, as execd reads it: /files?path=.
	if r.URL.RawQuery != "" {
		path += "?" + r.URL.RawQuery
	}
	response, err := t.call(ctx, relayFrame{WorkID: workID, Method: r.Method, Path: path, Token: token, Body: body})
	if err != nil {
		status := http.StatusServiceUnavailable
		if errors.Is(err, context.DeadlineExceeded) {
			status = http.StatusGatewayTimeout
		}
		http.Error(w, "computer did not answer", status)
		return
	}
	if response.Status < 100 || response.Status > 599 {
		http.Error(w, "computer answered badly", http.StatusBadGateway)
		return
	}
	if len(response.Body) > 0 {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(response.Status)
	_, _ = w.Write(response.Body)
}

// controlled reports whether a call is the platform's, authorized by the
// control token rather than the Work's execd token: the machine's
// reconciliation, opening the Work's environment, delivering or forgetting
// its credentials, and delivering its inputs. Every other call, another
// method on these paths included, carries the execd token and the machine
// answers it.
func controlled(workID, method, path string) bool {
	if workID == "" {
		return path == reconciliationPath && (method == http.MethodGet || method == http.MethodPost)
	}
	switch path {
	case "/", "/inputs":
		return method == http.MethodPut
	case "/credentials":
		return method == http.MethodPut || method == http.MethodDelete
	}
	return false
}

// pipePath reads the process of a pipe path, /pty/<id>/ws.
func pipePath(path string) (string, bool) {
	rest, ok := strings.CutPrefix(path, "/pty/")
	if !ok {
		return "", false
	}
	process, ok := strings.CutSuffix(rest, "/ws")
	return process, ok && process != "" && !strings.Contains(process, "/")
}

// servePipe opens the pipe on the machine first, so a refused attach answers
// with its status as execd's does, then relays frames both ways.
func (g *Gateway) servePipe(w http.ResponseWriter, r *http.Request, t *tunnel, open relayFrame) {
	ctx, cancel := context.WithCancel(context.WithoutCancel(r.Context()))
	defer cancel()
	id, frames, err := t.open(ctx, open)
	if err != nil {
		http.Error(w, "computer not connected", http.StatusServiceUnavailable)
		return
	}
	defer t.endStream(context.WithoutCancel(ctx), id)
	opening, waiting := context.WithTimeout(r.Context(), callTimeout)
	defer waiting()
	var first streamFrame
	select {
	case frame, ok := <-frames:
		if !ok || frame.closed {
			status := http.StatusServiceUnavailable
			if ok && frame.status >= 400 && frame.status <= 599 {
				status = frame.status
			}
			http.Error(w, "attach refused", status)
			return
		}
		first = frame
	case <-opening.Done():
		http.Error(w, "computer did not answer", http.StatusGatewayTimeout)
		return
	}
	client, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	client.SetReadLimit(readLimit)
	defer client.CloseNow()
	go func() {
		defer cancel()
		for {
			kind, data, err := client.Read(ctx)
			if err != nil {
				return
			}
			if err := t.writeStream(ctx, id, kind, data); err != nil {
				return
			}
		}
	}()
	for frame := first; ; {
		if frame.closed {
			_ = client.Close(websocket.StatusNormalClosure, "")
			return
		}
		if err := client.Write(ctx, frame.kind, frame.data); err != nil {
			return
		}
		var ok bool
		select {
		case frame, ok = <-frames:
			if !ok {
				_ = client.Close(websocket.StatusGoingAway, "stream ended")
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

// serveFile reads one file of the Work root through the tunnel as a stream
// and serves its bytes as one streamed response. The status of a refusal
// before any data answers the call; a stream lost, failed or idle past
// FileIdle after data aborts the response, so the reader never takes a
// truncated file for a whole one.
func (g *Gateway) serveFile(w http.ResponseWriter, r *http.Request, t *tunnel, open relayFrame) {
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	id, frames, err := t.open(ctx, open)
	if err != nil {
		http.Error(w, "computer not connected", http.StatusServiceUnavailable)
		return
	}
	defer t.endStream(context.WithoutCancel(ctx), id)
	opening, waiting := context.WithTimeout(ctx, callTimeout)
	defer waiting()
	var frame streamFrame
	select {
	case first, ok := <-frames:
		if !ok {
			http.Error(w, "computer not connected", http.StatusServiceUnavailable)
			return
		}
		if first.closed && first.status != 0 {
			status := http.StatusBadGateway
			if first.status >= 400 && first.status <= 599 {
				status = first.status
			}
			http.Error(w, "file read refused", status)
			return
		}
		frame = first
	case <-opening.Done():
		http.Error(w, "computer did not answer", http.StatusGatewayTimeout)
		return
	}
	waiting()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.WriteHeader(http.StatusOK)
	controller := http.NewResponseController(w)
	idle := time.NewTimer(g.fileIdle)
	defer idle.Stop()
	for {
		if frame.closed {
			if frame.status != 0 {
				panic(http.ErrAbortHandler)
			}
			return
		}
		if frame.kind == websocket.MessageBinary {
			// A reader that stops taking bytes is cut off as a stalled
			// machine is; a server without deadlines keeps the context's.
			_ = controller.SetWriteDeadline(time.Now().Add(g.fileIdle))
			if _, err := w.Write(frame.data); err != nil {
				return
			}
			_ = controller.Flush()
			// The machine sends more as the reader takes bytes; a lost
			// connection ends the stream and so the response.
			if err := t.credit(ctx, id, len(frame.data)); err != nil {
				g.logger.WarnContext(ctx, "computer gateway: file read credit not sent", "work_id", open.WorkID, "error", err)
			}
		}
		idle.Reset(g.fileIdle)
		var ok bool
		select {
		case frame, ok = <-frames:
			if !ok {
				panic(http.ErrAbortHandler)
			}
		case <-idle.C:
			g.logger.WarnContext(ctx, "computer gateway: file read stalled", "work_id", open.WorkID)
			panic(http.ErrAbortHandler)
		case <-ctx.Done():
			return
		}
	}
}

func (g *Gateway) tunnelOf(computerID string) *tunnel {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.tunnels[computerID]
}

func (g *Gateway) holdTunnel(computerID string, t *tunnel) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.tunnels[computerID] = t
}

func (g *Gateway) dropTunnel(computerID string, t *tunnel) {
	g.mu.Lock()
	if g.tunnels[computerID] == t {
		delete(g.tunnels, computerID)
	}
	g.mu.Unlock()
	t.close()
}
