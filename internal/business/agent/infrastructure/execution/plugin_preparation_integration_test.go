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

	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"
	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/pkg/execd"
	"golang.org/x/net/websocket"
	"xorm.io/xorm"
)

func TestPluginPreparationRecoveryRetainsHandleAndReportsDegradation(t *testing.T) {
	db, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	record := executionRecord{ID: uuid.NewString(), SessionID: uuid.NewString(), SandboxID: "sandbox", Version: 1, PreparationCommand: "native plugin preparation", RequestJSON: []byte(`{}`), SpecificationJSON: []byte(`{}`), CredentialPlanJSON: []byte(`{}`), ServiceAccessJSON: []byte(`[]`), AuthorizationIDs: []byte(`[]`)}
	_, err = db.Insert(&record)
	require.NoError(t, err)
	var reservations, attachments atomic.Int32
	// Interrupt observation only after attachment proves the handle was committed.
	// The deadline bounds a broken test; it does not schedule the interruption.
	observation, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	attachedHandle := make(chan string, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ping", func(http.ResponseWriter, *http.Request) {})
	mux.HandleFunc("POST /pty", func(w http.ResponseWriter, r *http.Request) {
		reservations.Add(1)
		var request struct{ Cwd, Command string }
		if json.NewDecoder(r.Body).Decode(&request) != nil || request.Command != "talgent_checkout_plan='null'; native plugin preparation" || request.Cwd != "/workspace" {
			http.Error(w, "unexpected command", 400)
			return
		}
		_, _ = w.Write([]byte(`{"session_id":"prepare-1"}`))
	})
	mux.Handle("GET /pty/prepare-1/ws", websocket.Handler(func(ws *websocket.Conn) {
		defer ws.Close()
		persisted, found, err := latestAllocation(t.Context(), db, record.SessionID)
		if err != nil || !found {
			return
		}
		_ = websocket.Message.Send(ws, `{"type":"connected","session_id":"prepare-1","mode":"pipe","role":"holder"}`)
		if attachments.Add(1) == 1 {
			attachedHandle <- persisted.PreparationProcessID
			cancel()
			var input []byte
			_ = websocket.Message.Receive(ws, &input) // cancellation detaches the observer
			return
		}
		_ = websocket.Message.Send(ws, append([]byte{1}, []byte(`{"results":[{"slug":"runtime","installed":false,"reason":"installation_failed"}]}`)...))
		_ = websocket.Message.Send(ws, `{"type":"exit","exit_code":0}`)
	}))
	server := httptest.NewServer(mux)
	defer server.Close()
	client, err := execd.NewClient(server.Client(), server.URL, "fixture-token")
	require.NoError(t, err)
	n := &NativeConnections{provisioning: &Provisioning{records: records{db: db}, now: time.Now}}
	launch := domain.LaunchSpecification{Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}, Plugins: []domain.SelectedPlugin{{Release: domain.PluginRelease{CatalogName: "talgent", Slug: "runtime"}}}}}
	_, err = n.prepareEnvironment(observation, &record, client, launch)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, "prepare-1", <-attachedHandle, "the handle is durable before observation stops")
	require.Equal(t, "prepare-1", record.PreparationProcessID)
	require.False(t, record.PreparationEnded)
	require.Empty(t, record.ProcessID)
	retained, found, err := latestAllocation(t.Context(), db, record.SessionID)
	require.NoError(t, err)
	require.True(t, found)
	// A new reconciler instance uses the committed handle after restart.
	n = &NativeConnections{provisioning: &Provisioning{records: records{db: db}, now: time.Now}}
	report, err := n.prepareEnvironment(t.Context(), &retained, client, launch)
	require.NoError(t, err, "installation failure is a reported degradation, not a preparation retry")
	require.Equal(t, domain.PreparationReport{Installations: []domain.PluginInstallation{{Slug: "runtime", Reason: "installation_failed"}}, Checkouts: []domain.RepositoryCheckout{}}, report)
	require.EqualValues(t, 1, reservations.Load())
	require.False(t, retained.PreparationEnded, "the execution records its preparation ended with the reported outcomes")
	// The execution records its preparation ended once the step returned.
	_, err = db.Exec("UPDATE agent_execution SET preparation_ended = 1 WHERE id = ?", record.ID)
	require.NoError(t, err)
	retained, _, err = latestAllocation(t.Context(), db, record.SessionID)
	require.NoError(t, err)
	report, err = n.prepareEnvironment(t.Context(), &retained, client, launch)
	require.NoError(t, err)
	require.Zero(t, report.Installations, "a completed preparation reports nothing again")
	require.Zero(t, report.Checkouts)
	require.EqualValues(t, 2, attachments.Load(), "completed preparation is not attached again")
}

func TestPluginPreparationDeletionRecoversAfterLostRetirement(t *testing.T) {
	db, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	p := &Provisioning{records: records{db: db}, now: time.Now}
	n := &NativeConnections{provisioning: p}
	record := executionRecord{ID: uuid.NewString(), SessionID: uuid.NewString(), SandboxID: "sandbox", Version: 1, PreparationProcessID: "preparing", PreparationCommand: "native plugin preparation", RequestJSON: []byte(`{}`), SpecificationJSON: []byte(`{}`), CredentialPlanJSON: []byte(`{}`), ServiceAccessJSON: []byte(`[]`), AuthorizationIDs: []byte(`[]`)}
	_, err = db.Insert(&record)
	require.NoError(t, err)
	var unavailable, deleted atomic.Bool
	discarding, interrupt := context.WithCancel(t.Context())
	defer interrupt()
	var calls atomic.Int32
	unavailable.Store(true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method == "GET" && r.URL.Path == "/pty/preparing" {
			if deleted.Load() {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write([]byte(`{"session_id":"preparing","running":false}`))
			return
		}
		if r.Method != "DELETE" || r.URL.Path != "/pty/preparing" || r.Header.Get("X-EXECD-ACCESS-TOKEN") != "fixture-token" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if unavailable.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if deleted.Swap(true) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		// Deletion succeeded, but the caller stops before it can retire the
		// preparation handle.
		interrupt()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	client, err := execd.NewClient(server.Client(), server.URL, "fixture-token")
	require.NoError(t, err)
	require.Error(t, n.discardPluginPreparation(t.Context(), &record, client))
	require.Equal(t, "preparing", record.PreparationProcessID)
	require.False(t, deleted.Load(), "provider failure is not deletion confirmation")
	unavailable.Store(false)
	require.ErrorIs(t, n.discardPluginPreparation(discarding, &record, client), context.Canceled)
	require.True(t, deleted.Load())
	retained, found, err := p.get(t.Context(), record.ID)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "preparing", retained.PreparationProcessID, "uncommitted retirement leaves the recovery obligation")
	recovered := &NativeConnections{provisioning: &Provisioning{records: records{db: db}, now: time.Now}}
	require.NoError(t, recovered.discardPluginPreparation(t.Context(), &retained, client))
	after, found, err := p.get(t.Context(), record.ID)
	require.NoError(t, err)
	require.True(t, found)
	require.Empty(t, after.PreparationProcessID)
	require.Empty(t, after.ProcessID, "preparation cleanup does not invent a Harness process or exit")
	require.False(t, after.PreparationEnded, "cancellation is not successful preparation")
	require.Zero(t, after.DeletedAt, "compute removal remains with its existing lifecycle")
	before := calls.Load()
	require.NoError(t, recovered.discardPluginPreparation(t.Context(), &after, client))
	require.Equal(t, before, calls.Load(), "a committed retirement does not repeat provider deletion")
}
