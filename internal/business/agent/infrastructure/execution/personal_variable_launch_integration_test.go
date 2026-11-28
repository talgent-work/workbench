//go:build integration

package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	jimuxorm "github.com/go-jimu/contrib/transaction/xorm"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/contracts/gen/go/dispatcher/private/v1/dispatcherprivatev1connect"
	"golang.org/x/net/websocket"
	"xorm.io/xorm"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure/sealing/sealingtest"
	"github.com/talgent-work/workbench/internal/testsupport/connecttest"
	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"
)

// variableExecd is a sandbox's execd as a Cloud run's steps reach it: it runs
// the preparation, takes file uploads and reserves the Harness process,
// recording each request in order. Its first Harness reservation fails when
// failLaunch is set.
type variableExecd struct {
	mu                 sync.Mutex
	token              string
	launch, prepare    string
	failLaunch         bool
	calls              []string
	uploads            []variableUpload
	launchAttempts     int
	preparationStarted bool
}

type variableUpload struct {
	Path    string
	Mode    int
	Content []byte
}

func (e *variableExecd) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && r.URL.Path == "/ping" {
		return
	}
	if r.URL.Path == "/pty/preparation-handle/ws" {
		websocket.Handler(func(ws *websocket.Conn) {
			defer ws.Close()
			_ = websocket.Message.Send(ws, `{"type":"connected","session_id":"preparation-handle","mode":"pipe","role":"holder"}`)
			_ = websocket.Message.Send(ws, append([]byte{1}, []byte(`{"results":[]}`)...))
			_ = websocket.Message.Send(ws, `{"type":"exit","exit_code":0}`)
		}).ServeHTTP(w, r)
		return
	}
	if r.Header.Get("X-EXECD-ACCESS-TOKEN") != e.token {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/files/upload":
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var upload variableUpload
		meta, _, err := r.FormFile("metadata")
		if err == nil {
			err = json.NewDecoder(meta).Decode(&upload)
		}
		file, _, fileErr := r.FormFile("file")
		if err != nil || fileErr != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		upload.Content, _ = io.ReadAll(file)
		e.uploads = append(e.uploads, upload)
		e.calls = append(e.calls, "upload")
	case r.Method == http.MethodPost && r.URL.Path == "/pty":
		var request struct{ Cwd, Command string }
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Cwd != "/workspace" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		switch request.Command {
		case e.prepare:
			e.calls = append(e.calls, "prepare")
			_, _ = w.Write([]byte(`{"session_id":"preparation-handle"}`))
		case e.launch:
			e.calls = append(e.calls, "launch")
			e.launchAttempts++
			if e.failLaunch && e.launchAttempts == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			_, _ = w.Write([]byte(`{"session_id":"process-handle"}`))
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// A Cloud run's Personal Variables reach its Harness launch only: they are
// uploaded, readable by the sandbox user alone, right before the Harness
// starts and after preparation, never in the sandbox's creation request, its
// stored specification or any command's text; a reserved name never travels.
// A retried launch hands over the same values, and the run records and
// answers the platform variables they replaced.
func TestTheHarnessLaunchAloneCarriesTheRunsPersonalVariables(t *testing.T) {
	ctx := context.Background()
	db, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	jdb, err := jimuxorm.New(db)
	require.NoError(t, err)
	provider, _, lifecycle := sandboxClient(t)
	provisioning := NewProvisioning(jdb, provider, fixtureSandboxCredentials(db, jdb, provider), NativeLaunchConfig{Binary: "claude"}, time.Now, nil)
	attachNativeCredentials(t, db, provisioning, lifecycle)

	pem := "-----BEGIN KEY-----\nAAAA\n-----END KEY-----\n"
	variables := []domain.PersonalVariable{
		{Name: "API_TOKEN", Value: "secret-token-value"},
		{Name: repositoryInstructionsVariable, Value: "0"},
		{Name: "GH_PROMPT_DISABLED", Value: "person-choice"},
		{Name: "PEM", Value: pem},
		// A reserved name never travels, even if a run holds one.
		{Name: "TALGENT_WORK_ID", Value: "replaced-work"},
	}
	input := request()
	input.WorkID = uuid.NewString()
	run, err := domain.BeginExecution(domain.ExecutionBeginning{
		Admitted: domain.WorkStartAdmitted{WorkID: input.WorkID, StartAdmittedAt: provisioning.now(), DeskID: "desk-" + uuid.NewString()},
		Launch:   input, Realization: domain.RealizationCloud, PersonalVariables: variables,
	})
	require.NoError(t, err)
	require.NoError(t, NewAgentExecutionRepository(jdb, sealingtest.Sealer(), time.Now).Save(ctx, run))
	record, _, err := provisioning.get(ctx, run.ID)
	require.NoError(t, err)
	require.NoError(t, provisioning.specify(ctx, record, command.CreationSupply{Mounts: testMounts}))
	record, _, err = provisioning.get(ctx, run.ID)
	require.NoError(t, err)
	var spec struct{ Env map[string]string }
	require.NoError(t, json.Unmarshal(record.SpecificationJSON, &spec))

	execd := &variableExecd{token: spec.Env["EXECD_ACCESS_TOKEN"], launch: record.LaunchCommand, prepare: withCheckoutPlan(record.PreparationCommand, []byte("null")), failLaunch: true}
	processAPI := httptest.NewServer(execd)
	defer processAPI.Close()
	lifecycle.endpoint = processAPI.URL
	mux := http.NewServeMux()
	receiver := &nativeConnectionReceiver{provisioning: provisioning}
	mux.Handle("/"+dispatcherprivatev1connect.ExecutionServiceName+"/", connecttest.Handler(func(s *connect.Server) { dispatcherprivatev1connect.RegisterExecutionServiceHandler(s, receiver) }))
	dispatcher := httptest.NewServer(mux)
	defer dispatcher.Close()
	connections, err := NewNativeConnections(provisioning, provisioning.credentials, dispatcher.Client(), dispatcher.URL, "dispatcher-secret")
	require.NoError(t, err)
	steps := newExecutionSteps(t, provisioning, connections, unread{}, nil, time.Now)

	_ = steps.pass(ctx, jdb)
	for range 3 {
		if launched := current(t, steps, run.ID); launched.Process.ID != "" {
			break
		}
		_ = steps.pass(ctx, jdb)
	}
	launched := current(t, steps, run.ID)
	require.Equal(t, "process-handle", launched.Process.ID, "the retried launch starts the Harness")

	execd.mu.Lock()
	calls, uploads := append([]string(nil), execd.calls...), append([]variableUpload(nil), execd.uploads...)
	execd.mu.Unlock()
	require.Equal(t, []string{"prepare", "upload", "launch", "upload", "launch"}, calls, "the values arrive after preparation, right before each launch attempt")
	file := sandboxVariableFile(SandboxOwner{WorkID: input.WorkID, ID: run.ID})
	want := []byte("API_TOKEN=secret-token-value\x00" + repositoryInstructionsVariable + "=0\x00GH_PROMPT_DISABLED=person-choice\x00PEM=" + pem + "\x00")
	for _, upload := range uploads {
		require.Equal(t, file, upload.Path, "outside every Work Directory, named from the execution alone")
		require.Equal(t, 600, upload.Mode)
		require.Equal(t, want, upload.Content, "the retried launch hands over the run's own values")
	}
	require.Contains(t, record.LaunchCommand, file, "the launch command loads the run's file")

	created, err := json.Marshal(lifecycle.create)
	require.NoError(t, err)
	for name, surface := range map[string][]byte{"creation request": created, "stored specification": record.SpecificationJSON, "launch command": []byte(record.LaunchCommand), "preparation command": []byte(record.PreparationCommand), "launch specification": record.RequestJSON} {
		for _, value := range []string{"secret-token-value", "person-choice", "AAAA", "replaced-work"} {
			require.False(t, bytes.Contains(surface, []byte(value)), "no value reaches the %s", name)
		}
	}

	require.Equal(t, []string{repositoryInstructionsVariable, "GH_PROMPT_DISABLED"}, launched.ReplacedVariables, "the platform's own variables they replaced, the reserved one excepted")
	require.Equal(t, variables, launched.PersonalVariables, "the run keeps its values")
	replaced, err := NewExecutionQueryRepository(jdb).ReadReplacedVariables(ctx, input.WorkID)
	require.NoError(t, err)
	require.Equal(t, launched.ReplacedVariables, replaced)
}
