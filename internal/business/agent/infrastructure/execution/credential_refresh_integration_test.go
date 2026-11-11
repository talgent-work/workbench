//go:build integration

package execution

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/contracts/gen/go/dispatcher/private/v1/dispatcherprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/testsupport/authorizationcatalogtest"
	"github.com/talgent-work/workbench/internal/testsupport/connecttest"
	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"
	"golang.org/x/net/websocket"
	"xorm.io/xorm"
)

// countingRepositoryCredentials counts how often a delivery asks Marketplace
// for repository credentials; each ask queries GitHub live.
type countingRepositoryCredentials struct {
	authorizationcatalogtest.NoRepositories
	resolves atomic.Int32
}

func (c *countingRepositoryCredentials) Resolve(ctx context.Context, owner, work string) (command.RepositoryAccessResult, error) {
	c.resolves.Add(1)
	return c.NoRepositories.Resolve(ctx, owner, work)
}

func TestConnectedWorkReconnectsWithoutDeliveringCredentials(t *testing.T) {
	ctx := context.Background()
	db, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	provider, _, lifecycle := sandboxClient(t)
	provisioning := NewProvisioning(db, provider, fixtureSandboxCredentials(db, provider), NativeLaunchConfig{Binary: "claude"}, time.Now, nil)
	attachNativeCredentials(t, db, provisioning, lifecycle)
	input := request()
	record, err := register(ctx, provisioning, input, testMounts)
	require.NoError(t, err)
	var spec struct{ Env map[string]string }
	require.NoError(t, json.Unmarshal(record.SpecificationJSON, &spec))
	processAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/ping" {
			return // execd answers
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
		var body struct{ Command string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("X-EXECD-ACCESS-TOKEN") != spec.Env["EXECD_ACCESS_TOKEN"] {
			w.WriteHeader(400)
			return
		}
		if body.Command == record.LaunchCommand {
			_, _ = w.Write([]byte(`{"session_id":"process-handle"}`))
			return
		}
		_, _ = w.Write([]byte(`{"session_id":"preparation-handle"}`))
	}))
	defer processAPI.Close()
	lifecycle.endpoint = processAPI.URL
	receiver := &nativeConnectionReceiver{provisioning: provisioning}
	mux := http.NewServeMux()
	path, handler := "/"+dispatcherprivatev1connect.ExecutionServiceName+"/", connecttest.Handler(func(s *connect.Server) { dispatcherprivatev1connect.RegisterExecutionServiceHandler(s, receiver) })
	mux.Handle(path, handler)
	dispatcher := httptest.NewServer(mux)
	defer dispatcher.Close()
	connections, err := NewNativeConnections(provisioning, provisioning.credentials, dispatcher.Client(), dispatcher.URL, "dispatcher-secret")
	require.NoError(t, err)
	steps := newExecutionSteps(t, provisioning, connections, unread{}, nil, time.Now)
	counter := &countingRepositoryCredentials{}
	supplies := command.NewEnvironmentSupplies(counter, authorizationcatalogtest.NoServices{}, directoryMounts{root: t.TempDir()}, &commandtest.Accounts{})
	steps.handler = command.NewReconcileExecutionHandler(time.Now, fixtureLease, fixtureCreationWaits, steps.executions, steps.environment, steps.harness, steps.desks, supplies, nil)
	// Submission with the first delivery, then preparation with the Harness
	// start and its connection.
	for range 2 {
		require.NoError(t, steps.pass(ctx, db))
	}
	healthy, _, err := latestAllocation(ctx, provisioning.db, input.WorkID)
	require.NoError(t, err)
	require.True(t, healthy.CredentialsDelivered && healthy.PreparationEnded && healthy.ProcessID != "", "Work reached a healthy native connection")

	resolved := counter.resolves.Load()
	vaultWrites := lifecycle.vault.state.Revision
	connects := len(receiver.requests)
	for range 5 {
		require.NoError(t, steps.pass(ctx, db))
	}
	require.Equal(t, resolved, counter.resolves.Load(), "a connected Work's reconnects never ask for repository credentials; each ask queries GitHub live")
	require.Equal(t, vaultWrites, lifecycle.vault.state.Revision, "a reconnect delivers nothing to the Vault")
	require.Len(t, receiver.requests, connects+5, "each reconnect only connects")
}
