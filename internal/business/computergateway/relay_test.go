package computergateway

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/coder/websocket"
	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/gen/go/talgent/computer/private/v1/computerprivatev1connect"
	"github.com/talgent-work/workbench/internal/pkg/computerconnection"
	"github.com/talgent-work/workbench/internal/pkg/execd"
)

// replicas are two Gateway replicas sharing one Redis, as in a cluster.
type replicas struct {
	a, b    *httptest.Server
	machine *websocket.Conn
}

func startReplicas(t *testing.T) *replicas {
	t.Helper()
	return startReplicasIdle(t, testFileIdle)
}

// startReplicasIdle starts replicas whose file reads wait fileIdle for each
// piece.
func startReplicasIdle(t *testing.T, fileIdle time.Duration) *replicas {
	t.Helper()
	stand := &computer{}
	backendMux := http.NewServeMux()
	backendMux.Handle(computerprivatev1connect.NewConnectionServiceHandler(stand))
	backend := httptest.NewServer(backendMux)
	t.Cleanup(backend.Close)
	rdb := goredis.NewClient(&goredis.Options{Addr: miniredis.RunT(t).Addr()})
	start := func(name string) *httptest.Server {
		server := httptest.NewUnstartedServer(nil)
		gateway := NewGateway(computerprivatev1connect.NewConnectionServiceClient(http.DefaultClient, backend.URL), computerconnection.NewWriter(rdb), computerconnection.NewReader(rdb),
			Replica{Name: name, Endpoint: server.Listener.Addr().String(), ControlToken: "control"}, slog.Default())
		gateway.fileIdle = fileIdle
		mux := http.NewServeMux()
		mux.HandleFunc("GET /computer/v1/connect", gateway.ServeConnect)
		mux.HandleFunc("/relay/{computer}/works/{work}", gateway.ServeRelay)
		mux.HandleFunc("/relay/{computer}/works/{work}/{rest...}", gateway.ServeRelay)
		mux.HandleFunc("/relay/{computer}/reconciliation", gateway.ServeMachineRelay)
		server.Config.Handler = mux
		server.Start()
		t.Cleanup(server.Close)
		return server
	}
	r := &replicas{a: start("a"), b: start("b")}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	machine, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(r.a.URL, "http")+"/computer/v1/connect",
		&websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer good"}, HeaderTalgentComputerVersion: {"0.1.0"}}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = machine.CloseNow() })
	r.machine = machine
	_, _, err = machine.Read(ctx) // welcome
	require.NoError(t, err)
	go serveMachine(machine)
	// The Gateway sends welcome before it relays through the connection: the
	// tests start once a relayed call reaches the machine.
	require.Eventually(t, func() bool {
		response, err := http.Get(r.a.URL + "/relay/c1/works/w1/ping")
		if err != nil {
			return false
		}
		_ = response.Body.Close()
		return response.StatusCode != http.StatusServiceUnavailable
	}, 5*time.Second, 10*time.Millisecond)
	return r
}

// serveMachine plays a Talgent Computer with one Work process "p1" that
// echoes its stdin: execd answers, and a pipe with replay from its offset.
func serveMachine(conn *websocket.Conn) {
	ctx := context.Background()
	output := []byte("hello\n")
	attached := false
	// credits route each file read's credit to the goroutine sending it.
	credits := map[string]chan int{}
	send := func(v any) { data, _ := json.Marshal(v); _ = conn.Write(ctx, websocket.MessageText, data) }
	for {
		kind, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		if kind == websocket.MessageBinary {
			id, frame := data[:16], data[16:]
			if frame[0] == 0 { // stdin, echoed as stdout
				_ = conn.Write(ctx, websocket.MessageBinary, append(append([]byte{}, id...), append([]byte{1}, frame[1:]...)...))
			}
			continue
		}
		var f relayFrame
		_ = json.Unmarshal(data, &f)
		switch f.Type {
		case "exec_request":
			switch {
			case f.Method == http.MethodPut && f.Path == "/":
				send(relayFrame{Type: "exec_response", RequestID: f.RequestID, Status: 200, Body: json.RawMessage(`{"work_root":"/home/u/works/w1"}`)})
			case (f.Method == http.MethodPut || f.Method == http.MethodDelete) && f.Path == "/credentials" && f.Token == "":
				send(relayFrame{Type: "exec_response", RequestID: f.RequestID, Status: http.StatusNoContent})
			case f.Method == http.MethodPut && f.Path == "/inputs" && f.Token == "":
				send(relayFrame{Type: "exec_response", RequestID: f.RequestID, Status: http.StatusAccepted})
			case f.WorkID == "" && f.Path == "/reconciliation" && f.Token == "" && f.Method == http.MethodGet:
				send(relayFrame{Type: "exec_response", RequestID: f.RequestID, Status: 200, Body: json.RawMessage(`{"round":"r1","reconciled":false,"works":[]}`)})
			case f.WorkID == "" && f.Path == "/reconciliation" && f.Token == "" && f.Method == http.MethodPost && string(f.Body) == `{"round":"r1"}`:
				send(relayFrame{Type: "exec_response", RequestID: f.RequestID, Status: http.StatusNoContent})
			case f.Token != "token":
				send(relayFrame{Type: "exec_response", RequestID: f.RequestID, Status: 401})
			case f.Method == http.MethodGet && f.Path == "/files?path=docs":
				send(relayFrame{Type: "exec_response", RequestID: f.RequestID, Status: 200, Body: json.RawMessage(`{"entries":[{"name":"a.md","kind":"file","size":3,"modified_at":"2026-11-02T10:00:00Z"}]}`)})
			case f.Method == http.MethodPost && f.Path == "/pty":
				send(relayFrame{Type: "exec_response", RequestID: f.RequestID, Status: 200, Body: json.RawMessage(`{"session_id":"p1"}`)})
			default:
				send(relayFrame{Type: "exec_response", RequestID: f.RequestID, Status: 404})
			}
		case "stream_open":
			if f.Kind == "file" {
				credit := make(chan int, 1024)
				credits[f.StreamID] = credit
				go serveFileRead(ctx, conn, f, credit)
				continue
			}
			if attached || f.ProcessID != "p1" {
				send(relayFrame{Type: "stream_close", StreamID: f.StreamID, Status: http.StatusConflict})
				continue
			}
			attached = true
			id := uuid.MustParse(f.StreamID)
			send(relayFrame{Type: "stream_text", StreamID: f.StreamID, Text: `{"type":"connected","session_id":"p1","mode":"pipe","role":"holder"}`})
			replay := make([]byte, 9, 9+len(output))
			replay[0] = 3
			binary.BigEndian.PutUint64(replay[1:], uint64(f.Since))
			replay = append(replay, output[f.Since:]...)
			_ = conn.Write(ctx, websocket.MessageBinary, append(id[:], replay...))
		case "stream_credit":
			select {
			case credits[f.StreamID] <- f.Bytes:
			default:
			}
		case "stream_close":
			if credit, ok := credits[f.StreamID]; ok {
				close(credit)
				delete(credits, f.StreamID)
				continue
			}
			attached = false
		}
	}
}

// machineFiles are the files of the fake machine's Work root: a missing
// path is refused, "broken.bin" fails after its first frame, and
// "stalled.bin" sends its first frame and nothing more.
// "huge.bin" is many times the frames a pipe may hold for its reader.
var machineFiles = map[string][]byte{
	"big.bin": bytes.Repeat([]byte("0123456789abcdef"), 3*machineChunk/16+5), "huge.bin": numbered(4 * streamBacklog * machineChunk),
	"empty.txt": {}, "broken.bin": []byte("part"), "stalled.bin": []byte("part"),
}

// machineChunk is the piece of a file read Talgent Computer sends in one frame.
const machineChunk = 64 << 10

// numbered is n bytes that tell any piece out of place.
func numbered(n int) []byte {
	content := make([]byte, n)
	for i := range content {
		content[i] = byte(i ^ i>>8 ^ i>>16)
	}
	return content
}

// testFileIdle is the relay's file idle bound in these tests: long enough
// that a slow, race-instrumented runner never pauses past it between frames,
// short enough that a stalled read still ends within its test's bound.
const testFileIdle = 2 * time.Second

// serveFileRead answers a file read stream as Talgent Computer does: raw
// bytes in binary frames within the credited window, then stream_close
// status 0, or a refusal first.
func serveFileRead(ctx context.Context, conn *websocket.Conn, f relayFrame, credits <-chan int) {
	send := func(v any) { data, _ := json.Marshal(v); _ = conn.Write(ctx, websocket.MessageText, data) }
	content, ok := machineFiles[f.Path]
	switch {
	case f.Token != "token":
		send(relayFrame{Type: "stream_close", StreamID: f.StreamID, Status: http.StatusUnauthorized})
		return
	case f.Path == "docs":
		send(relayFrame{Type: "stream_close", StreamID: f.StreamID, Status: http.StatusBadRequest})
		return
	case !ok:
		send(relayFrame{Type: "stream_close", StreamID: f.StreamID, Status: http.StatusNotFound})
		return
	}
	id := uuid.MustParse(f.StreamID)
	window := fileWindow
	for chunk := range slices.Chunk(content, machineChunk) {
		for window < len(chunk) {
			credit, ok := <-credits
			if !ok {
				return
			}
			window += credit
		}
		window -= len(chunk)
		_ = conn.Write(ctx, websocket.MessageBinary, append(id[:], chunk...))
	}
	if f.Path == "stalled.bin" {
		return
	}
	status := 0
	if f.Path == "broken.bin" {
		status = http.StatusInternalServerError
	}
	send(relayFrame{Type: "stream_close", StreamID: f.StreamID, Status: status})
}

func execdAt(t *testing.T, server *httptest.Server, token string) *execd.Client {
	t.Helper()
	client, err := execd.NewClient(http.DefaultClient, server.URL+"/relay/c1/works/w1", token)
	require.NoError(t, err)
	return client
}

func TestTheRelayServesAWorksExecdFromAnyReplica(t *testing.T) {
	r := startReplicas(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, server := range []*httptest.Server{r.a, r.b} {
		put, _ := http.NewRequestWithContext(ctx, http.MethodPut, server.URL+"/relay/c1/works/w1", strings.NewReader(`{"access_token":"token"}`))
		response, err := http.DefaultClient.Do(put)
		require.NoError(t, err)
		require.Equal(t, http.StatusUnauthorized, response.StatusCode, "opening an environment needs the control token")
		put, _ = http.NewRequestWithContext(ctx, http.MethodPut, server.URL+"/relay/c1/works/w1", strings.NewReader(`{"access_token":"token"}`))
		put.Header.Set(HeaderControlToken, "control")
		response, err = http.DefaultClient.Do(put)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, response.StatusCode)

		_, err = execdAt(t, server, "wrong").Create(ctx, "/workspace", "cat")
		require.ErrorContains(t, err, "HTTP 401")
		client := execdAt(t, server, "token")
		id, err := client.Create(ctx, "/workspace", "cat")
		require.NoError(t, err)
		require.Equal(t, "p1", id)

		var pipe *execd.Pipe
		require.Eventually(t, func() bool { pipe, err = client.Attach(ctx, id, 2); return err == nil }, 5*time.Second, 50*time.Millisecond)
		frame, err := pipe.Receive(ctx)
		require.NoError(t, err)
		require.Equal(t, "connected", frame.Type)
		frame, err = pipe.Receive(ctx)
		require.NoError(t, err)
		require.Equal(t, "replay", frame.Type)
		require.Equal(t, int64(2), frame.Offset)
		require.Equal(t, "llo\n", string(frame.Data))
		_, err = client.Attach(ctx, id, 0)
		require.Error(t, err, "a second attach is refused while one holds the process")
		require.NoError(t, pipe.WriteInput(ctx, []byte("ping")))
		frame, err = pipe.Receive(ctx)
		require.NoError(t, err)
		require.Equal(t, "stdout", frame.Type)
		require.Equal(t, "ping", string(frame.Data))
		require.NoError(t, pipe.Close())
		var again *execd.Pipe
		require.Eventually(t, func() bool {
			again, err = client.Attach(ctx, id, 0)
			return err == nil
		}, 5*time.Second, 50*time.Millisecond, "a detached process can be attached again")
		require.NoError(t, again.Close())
	}
}

func TestTheRelayAnswersUnavailableWithoutAConnection(t *testing.T) {
	r := startReplicas(t)
	_ = r.machine.Close(websocket.StatusNormalClosure, "")
	require.Eventually(t, func() bool {
		response, err := http.Get(r.b.URL + "/relay/c1/works/w1/ping")
		if err != nil {
			return false
		}
		defer response.Body.Close()
		return response.StatusCode == http.StatusServiceUnavailable
	}, 5*time.Second, 50*time.Millisecond)
}

// Delivering and forgetting a Work's credentials are the platform's calls,
// authorized by the control token and relayed without an execd token; any
// other call on that path is the Work's and the machine answers it.
func TestTheRelayChecksTheControlTokenOfCredentialDelivery(t *testing.T) {
	r := startReplicas(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	call := func(server *httptest.Server, method string, header http.Header) int {
		t.Helper()
		request, err := http.NewRequestWithContext(ctx, method, server.URL+"/relay/c1/works/w1/credentials", strings.NewReader(`{"gateway":{"work_id":"w1"}}`))
		require.NoError(t, err)
		request.Header = header
		response, err := http.DefaultClient.Do(request)
		require.NoError(t, err)
		defer response.Body.Close()
		return response.StatusCode
	}
	for _, server := range []*httptest.Server{r.a, r.b} {
		for _, method := range []string{http.MethodPut, http.MethodDelete} {
			require.Equal(t, http.StatusUnauthorized, call(server, method, http.Header{execdTokenHeader: {"token"}}), "%s needs the control token, not the Work's", method)
			require.Equal(t, http.StatusUnauthorized, call(server, method, http.Header{HeaderControlToken: {"wrong"}}), method)
			require.Equal(t, http.StatusNoContent, call(server, method, http.Header{HeaderControlToken: {"control"}, execdTokenHeader: {"token"}}), "%s is relayed with an empty token", method)
		}
		require.Equal(t, http.StatusUnauthorized, call(server, http.MethodGet, http.Header{HeaderControlToken: {"control"}}), "another method carries the execd token")
		require.Equal(t, http.StatusNotFound, call(server, http.MethodGet, http.Header{execdTokenHeader: {"token"}}), "the machine answers another method")
	}
}

// A Work's files are read through the tunnel from any replica: a directory
// listing as a call carrying its query, a file's content as one streamed
// response, refusals with the machine's status, and a read that fails after
// its first bytes as an aborted response.
func TestTheRelayReadsAWorksFiles(t *testing.T) {
	r := startReplicas(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	get := func(server *httptest.Server, rest, token string) (*http.Response, []byte, error) {
		t.Helper()
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/relay/c1/works/w1"+rest, nil)
		require.NoError(t, err)
		request.Header.Set(execdTokenHeader, token)
		response, err := http.DefaultClient.Do(request)
		require.NoError(t, err)
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		return response, body, err
	}
	for _, server := range []*httptest.Server{r.a, r.b} {
		response, body, err := get(server, "/files?path=docs", "token")
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, response.StatusCode)
		require.JSONEq(t, `{"entries":[{"name":"a.md","kind":"file","size":3,"modified_at":"2026-11-02T10:00:00Z"}]}`, string(body))

		for name, content := range map[string][]byte{"big.bin": machineFiles["big.bin"], "empty.txt": {}} {
			response, body, err = get(server, "/files/content?path="+name, "token")
			require.NoError(t, err, name)
			require.Equal(t, http.StatusOK, response.StatusCode, name)
			require.Equal(t, "application/octet-stream", response.Header.Get("Content-Type"))
			require.Equal(t, content, body, name)
		}
		for path, status := range map[string]int{"missing.md": http.StatusNotFound, "docs": http.StatusBadRequest} {
			response, _, err = get(server, "/files/content?path="+path, "token")
			require.NoError(t, err)
			require.Equal(t, status, response.StatusCode, path)
		}
		response, _, err = get(server, "/files/content?path=big.bin", "wrong")
		require.NoError(t, err)
		require.Equal(t, http.StatusUnauthorized, response.StatusCode)

		_, _, err = get(server, "/files/content?path=broken.bin", "token")
		require.Error(t, err, "a read that fails after data is not a whole file")

		started := time.Now()
		_, _, err = get(server, "/files/content?path=stalled.bin", "token")
		require.Error(t, err, "a read the machine stops sending is aborted, not held open")
		require.Less(t, time.Since(started), 5*time.Second)
	}
}

// A file read follows its reader's pace: a reader far slower than the
// machine still takes a file many times what a pipe may hold, whole.
func TestTheRelayPacesAFileReadToItsReader(t *testing.T) {
	// The production idle bound: this long read under the race detector,
	// beside other packages' tests, may pause past the short test bound,
	// which only the stalled read needs.
	r := startReplicasIdle(t, FileIdle)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for _, server := range []*httptest.Server{r.a, r.b} {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/relay/c1/works/w1/files/content?path=huge.bin", nil)
		require.NoError(t, err)
		request.Header.Set(execdTokenHeader, "token")
		response, err := http.DefaultClient.Do(request)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, response.StatusCode)
		var body bytes.Buffer
		piece := make([]byte, 256<<10)
		for {
			n, err := response.Body.Read(piece)
			body.Write(piece[:n])
			if err == io.EOF {
				break
			}
			require.NoError(t, err)
			time.Sleep(time.Millisecond)
		}
		_ = response.Body.Close()
		require.True(t, bytes.Equal(machineFiles["huge.bin"], body.Bytes()), "the file arrives whole and in order")
	}
}

// Delivering a Work's inputs is the platform's call, as delivering its
// credentials is.
func TestTheRelayChecksTheControlTokenOfInputDelivery(t *testing.T) {
	r := startReplicas(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, server := range []*httptest.Server{r.a, r.b} {
		for header, status := range map[string]int{execdTokenHeader: http.StatusUnauthorized, HeaderControlToken: http.StatusAccepted} {
			request, err := http.NewRequestWithContext(ctx, http.MethodPut, server.URL+"/relay/c1/works/w1/inputs", strings.NewReader(`{"files":[]}`))
			require.NoError(t, err)
			request.Header.Set(header, map[string]string{execdTokenHeader: "token", HeaderControlToken: "control"}[header])
			response, err := http.DefaultClient.Do(request)
			require.NoError(t, err)
			_ = response.Body.Close()
			require.Equal(t, status, response.StatusCode, header)
		}
	}
}

// The machine's reconciliation is the platform's call, as delivering a
// Work's credentials is, and names no Work: from any replica it reaches the
// machine with the control token and an empty execd token, and only its GET
// and POST are relayed.
func TestTheRelayCarriesTheMachinesReconciliation(t *testing.T) {
	r := startReplicas(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	call := func(server *httptest.Server, method, body string, header http.Header) (int, string) {
		t.Helper()
		request, err := http.NewRequestWithContext(ctx, method, server.URL+"/relay/c1/reconciliation", strings.NewReader(body))
		require.NoError(t, err)
		request.Header = header
		response, err := http.DefaultClient.Do(request)
		require.NoError(t, err)
		defer response.Body.Close()
		answer, _ := io.ReadAll(response.Body)
		return response.StatusCode, string(answer)
	}
	control := http.Header{HeaderControlToken: {"control"}, execdTokenHeader: {"token"}}
	for _, server := range []*httptest.Server{r.a, r.b} {
		status, body := call(server, http.MethodGet, "", control)
		require.Equal(t, http.StatusOK, status)
		require.JSONEq(t, `{"round":"r1","reconciled":false,"works":[]}`, body)
		status, _ = call(server, http.MethodPost, `{"round":"r1"}`, control)
		require.Equal(t, http.StatusNoContent, status, "the decision is relayed with an empty token")
		status, _ = call(server, http.MethodGet, "", http.Header{execdTokenHeader: {"token"}})
		require.Equal(t, http.StatusUnauthorized, status, "a Work's token does not reconcile the machine")
		status, _ = call(server, http.MethodDelete, "", control)
		require.Equal(t, http.StatusMethodNotAllowed, status)
	}
}
