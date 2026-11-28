//go:build integration

package execution

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"

	"golang.org/x/net/websocket"
)

// machineRelay is Computer Gateway's relay to one Talgent Computer, serving
// the environment PUT and the execd subset for the Works on it.
type machineRelay struct {
	mu sync.Mutex
	// unreachable answers as the Gateway does without the machine's
	// connection.
	unreachable bool
	computerID  string
	token       string
	environment map[string]string
	modelProxy  bool
	// telemetry is the environment PUT's telemetry.
	telemetry map[string]string
	// credentials is the body of the last credentials PUT, nil once
	// forgotten.
	credentials           []byte
	deliveries, forgotten int
	files                 map[string]string
	created               []relayedProcess
	deleted               []string
	calls                 []string
	// reconciliation serves the machine's reconciliation; nil answers 404,
	// as a Talgent Computer from before reconciliation does. Its rounds'
	// state is what the ping reports; without it the round is decided,
	// unless undecided says otherwise.
	reconciliation http.Handler
	undecided      bool
	// preparation is the report the preparation process writes; empty
	// reports no Plugin and no checkout.
	preparation string
	// launching, when set, runs as the Harness process is created.
	launching func()
	// ignoresVariables answers a process creation as a Talgent Computer
	// that predates Personal Variables: without acknowledging them.
	ignoresVariables bool
	// replacedByMachine are the names a creation with Personal Variables
	// answers as the variables of the Work and of Talgent Computer they
	// replaced.
	replacedByMachine []string
	// awaiting is a Harness process whose Personal Variables a restart of
	// Talgent Computer lost before it ever ran; restored are the variables
	// given to it again. Without it the fixture keeps no process status and
	// answers a status call 404, as for an unknown process.
	awaiting bool
	restored []map[string]string
}

// relayedProcess is a process creation the machine was sent.
type relayedProcess struct {
	Cwd, Command      string
	PersonalVariables map[string]string `json:"personal_variables"`
}

// reconciled is the state of the machine's current round, as its ping
// reports it.
func (m *machineRelay) reconciled() bool {
	m.mu.Lock()
	rounds, undecided := m.reconciliation, m.undecided
	m.mu.Unlock()
	if decider, ok := rounds.(interface{ decided() bool }); ok {
		return decider.decided()
	}
	return !undecided
}

func (m *machineRelay) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	m.calls = append(m.calls, r.Method+" "+r.URL.Path)
	unreachable := m.unreachable
	m.mu.Unlock()
	if unreachable {
		http.Error(w, "computer not connected", http.StatusServiceUnavailable)
		return
	}
	if r.URL.Path == "/relay/"+m.computerID+"/reconciliation" && m.reconciliation != nil {
		m.reconciliation.ServeHTTP(w, r)
		return
	}
	prefix := "/relay/" + m.computerID + "/works/"
	rest, ok := strings.CutPrefix(r.URL.Path, prefix)
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	_, path, _ := strings.Cut(rest, "/")
	path = "/" + path
	if r.Method == http.MethodPut && path == "/" {
		if r.Header.Get("X-Talgent-Control-Token") != "computer-control" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var body struct {
			AccessToken string            `json:"access_token"`
			Environment map[string]string `json:"environment"`
			ModelProxy  bool              `json:"model_proxy"`
			Telemetry   map[string]string `json:"telemetry"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		m.mu.Lock()
		m.token, m.environment, m.modelProxy, m.telemetry = body.AccessToken, body.Environment, body.ModelProxy, body.Telemetry
		m.mu.Unlock()
		_, _ = w.Write([]byte(`{"work_root":"/home/me/.talgent/works/w"}`))
		return
	}
	if (r.Method == http.MethodPut || r.Method == http.MethodDelete) && path == "/credentials" {
		if r.Header.Get("X-Talgent-Control-Token") != "computer-control" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		m.mu.Lock()
		if r.Method == http.MethodPut {
			m.credentials = body
			m.deliveries++
		} else {
			m.credentials = nil
			m.forgotten++
		}
		m.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
		return
	}
	m.mu.Lock()
	token := m.token
	m.mu.Unlock()
	if token == "" || r.Header.Get("X-EXECD-ACCESS-TOKEN") != token {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	switch {
	case r.Method == http.MethodGet && path == "/ping":
		_ = json.NewEncoder(w).Encode(map[string]bool{"reconciled": m.reconciled()})
	case r.Method == http.MethodGet && path == "/files":
		m.mu.Lock()
		entries := []map[string]any{}
		for name, content := range m.files {
			entries = append(entries, map[string]any{"name": name, "kind": "file", "size": len(content), "modified_at": "2026-11-02T10:00:00Z"})
		}
		m.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"entries": entries})
	case r.Method == http.MethodGet && path == "/files/stat":
		m.mu.Lock()
		name := r.URL.Query().Get("path")
		content, ok := m.files[name]
		m.mu.Unlock()
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"name": name, "kind": "file", "size": len(content), "modified_at": "2026-11-02T10:00:00Z"})
	case r.Method == http.MethodGet && path == "/files/content":
		m.mu.Lock()
		content, ok := m.files[r.URL.Query().Get("path")]
		m.mu.Unlock()
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = io.WriteString(w, content)
	case r.Method == http.MethodPost && path == "/pty":
		var body relayedProcess
		_ = json.NewDecoder(r.Body).Decode(&body)
		m.mu.Lock()
		acknowledged := body.PersonalVariables != nil && !m.ignoresVariables
		m.created = append(m.created, body)
		// Only the Harness launch reads the native input stream; preparation
		// and the Harness's own hooks both run node.
		id := "preparation-handle"
		if strings.Contains(body.Command, "--input-format") {
			id = "harness-handle"
		}
		launching := m.launching
		m.mu.Unlock()
		if launching != nil && id == "harness-handle" {
			launching()
		}
		if acknowledged {
			m.mu.Lock()
			replaced := m.replacedByMachine
			m.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"session_id": id, "personal_variables": true, "replaced_variables": replaced})
			return
		}
		_, _ = w.Write([]byte(`{"session_id":"` + id + `"}`))
	case r.Method == http.MethodGet && path == "/pty/preparation-handle/ws":
		m.mu.Lock()
		report := m.preparation
		m.mu.Unlock()
		if report == "" {
			report = `{"results":[],"repositories":null}`
		}
		websocket.Handler(func(ws *websocket.Conn) {
			defer ws.Close()
			_ = websocket.Message.Send(ws, `{"type":"connected","session_id":"preparation-handle","mode":"pipe","role":"holder"}`)
			_ = websocket.Message.Send(ws, append([]byte{1}, []byte(report+"\n")...))
			_ = websocket.Message.Send(ws, `{"type":"exit","exit_code":0}`)
		}).ServeHTTP(w, r)
	case r.Method == http.MethodGet && path == "/pty/harness-handle":
		m.mu.Lock()
		awaiting := m.awaiting
		m.mu.Unlock()
		if !awaiting {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"session_id":"harness-handle","running":false,"awaiting_personal_variables":true}`))
	case r.Method == http.MethodPut && path == "/pty/harness-handle/personal_variables":
		var body struct {
			Variables map[string]string `json:"personal_variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		m.mu.Lock()
		m.restored = append(m.restored, body.Variables)
		m.awaiting = false
		m.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodDelete && strings.HasPrefix(path, "/pty/"):
		m.mu.Lock()
		m.deleted = append(m.deleted, strings.TrimPrefix(path, "/pty/"))
		m.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (m *machineRelay) set(change func(*machineRelay)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	change(m)
}

