//go:build integration

package execution

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/talgent-work/workbench/pkg/mysqltest"

	"connectrpc.com/connect"
	sdk "github.com/alibaba/OpenSandbox/sdks/sandbox/go"
	"github.com/stretchr/testify/require"
	dispatcherprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/dispatcher/private/v1"
	"github.com/talgent-work/workbench/gen/go/talgent/dispatcher/private/v1/dispatcherprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"xorm.io/xorm"
)

type nativeProcessObservation struct {
	dispatcherprivatev1connect.UnimplementedExecutionServiceHandler
	state dispatcherprivatev1.ProcessState
}

func (h nativeProcessObservation) ObserveProcess(_ context.Context, request *connect.Request[dispatcherprivatev1.ObserveProcessRequest]) (*connect.Response[dispatcherprivatev1.ObserveProcessResponse], error) {
	if request.Header().Get("Authorization") != "Bearer dispatcher-secret" || request.Msg.ProcessId != "current-process" {
		return nil, connect.NewError(connect.CodeInvalidArgument, domain.ErrInvalidWork)
	}
	return connect.NewResponse(&dispatcherprivatev1.ObserveProcessResponse{State: h.state}), nil
}

// An old deleted allocation is not evidence that the process of a newly
// admitted start exited, and only an exit fact naming the current binding's
// process is acknowledged on it.
func TestProcessObservationDistinguishesRetiredEnvironmentFromCurrentStart(t *testing.T) {
	db, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	sandboxes, _, lifecycle := sandboxClient(t)
	mux := http.NewServeMux()
	path, handler := dispatcherprivatev1connect.NewExecutionServiceHandler(nativeProcessObservation{state: dispatcherprivatev1.ProcessState_PROCESS_STATE_RUNNING})
	mux.Handle(path, handler)
	server := httptest.NewServer(mux)
	defer server.Close()
	observer, err := NewProcessObservations(NewProvisioning(db, sandboxes, nil, NativeLaunchConfig{}, time.Now, nil, nil), NewDispatcherControl(server.Client(), server.URL, "dispatcher-secret"))
	require.NoError(t, err)
	old := allocationRecord{ID: "old-allocation", SessionID: "work", SandboxID: sandboxID, ProcessID: "old-process", CreatedAt: 1, DeletedAt: 2, RequestJSON: []byte(`{}`), CredentialPlanJSON: []byte(`{}`), ServiceAccessJSON: []byte(`[]`), SpecificationJSON: []byte(`{}`)}
	_, err = db.Insert(&old)
	require.NoError(t, err)
	observed, err := observer.ObserveProcess(t.Context(), "work")
	require.NoError(t, err)
	require.Equal(t, domain.ProcessPending, observed.State, "absence of P1's environment cannot establish exit of an unlaunched P2")
	require.True(t, observed.ComputeReclaimed)
	current := allocationRecord{ID: "2b5cff9e-e3a6-4e56-9a2c-5aa9a8a7e006", SessionID: "work", SandboxID: sandboxID, ProcessID: "current-process", CreatedAt: 3, RequestJSON: []byte(`{}`), CredentialPlanJSON: []byte(`{}`), ServiceAccessJSON: []byte(`[]`), SpecificationJSON: []byte(`{}`)}
	_, err = db.Insert(&current)
	require.NoError(t, err)
	lifecycle.mu.Lock()
	lifecycle.info = &sdk.SandboxInfo{ID: sandboxID, Metadata: correlation(Allocation{ID: current.ID, WorkID: "work"})}
	lifecycle.mu.Unlock()
	observed, err = observer.ObserveProcess(t.Context(), "work")
	require.NoError(t, err)
	require.Equal(t, domain.ProcessRunning, observed.State)
	require.False(t, observed.ComputeReclaimed)
	acknowledged, err := observer.AcknowledgeExit(t.Context(), "work", "old-process")
	require.NoError(t, err)
	require.False(t, acknowledged, "P1's late exit fact is not P2's")
	acknowledged, err = observer.AcknowledgeExit(t.Context(), "work", "current-process")
	require.NoError(t, err)
	require.True(t, acknowledged)
	record, _, err := observer.provisioning.live(t.Context(), "work")
	require.NoError(t, err)
	require.True(t, record.ProcessExited)
	acknowledged, err = observer.AcknowledgeExit(t.Context(), "work", "current-process")
	require.NoError(t, err)
	require.True(t, acknowledged, "a redelivered exit fact is acknowledged again")
	lifecycle.mu.Lock()
	lifecycle.info = nil
	lifecycle.mu.Unlock()
	// Observing a live allocation's process asks only Dispatcher: the loss of
	// its sandbox is the environment authority's report to make.
	lifecycle.mu.Lock()
	before := lifecycle.requests
	lifecycle.mu.Unlock()
	observed, err = observer.ObserveProcess(t.Context(), "work")
	require.NoError(t, err)
	require.NotEqual(t, domain.ProcessEnvironmentLost, observed.State)
	lifecycle.mu.Lock()
	require.Equal(t, before, lifecycle.requests, "the environment authority is not asked")
	lifecycle.mu.Unlock()
}

// A delayed ReclaimCompute(workID) may execute after another caller finished
// P1 cleanup and resumed P2. The resource boundary must preserve live P2.
func TestLateComputeRequestCannotDeleteRunningCurrentProcess(t *testing.T) {
	db, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	sandboxes, _, lifecycle := sandboxClient(t)
	// Its Work still reaches the run, so only an exit authorizes the release.
	p := NewProvisioning(db, sandboxes, &SandboxCredentials{works: NewHeldWorks(db, &unreachedWorks{})}, NativeLaunchConfig{}, time.Now, nil, nil)
	current := allocationRecord{ID: "2b5cff9e-e3a6-4e56-9a2c-5aa9a8a7e006", SessionID: "work", SandboxID: sandboxID, ProcessID: "current-process", CreatedAt: 3, RequestJSON: []byte(`{}`), CredentialPlanJSON: []byte(`{}`), ServiceAccessJSON: []byte(`[]`), SpecificationJSON: []byte(`{}`)}
	_, err = db.Insert(&current)
	require.NoError(t, err)
	lifecycle.info = &sdk.SandboxInfo{ID: sandboxID, Metadata: correlation(Allocation{ID: current.ID, WorkID: "work"})}
	root, err := os.OpenRoot(t.TempDir())
	require.NoError(t, err)
	defer root.Close()
	removals := NewRemovals(db, time.Now)
	environments, err := NewEnvironments(p, removals, allocationObserverFixture(t, p, dispatcherprivatev1.ProcessState_PROCESS_STATE_RUNNING))
	require.NoError(t, err)
	require.NoError(t, environments.Delete(t.Context(), "work"))
	_, found, err := p.live(t.Context(), "work")
	require.NoError(t, err)
	require.True(t, found, "an old Work-level request cannot retire the current live allocation")
	pending, err := pendingRemovals(t.Context(), removals)
	require.NoError(t, err)
	require.Empty(t, pending, "an old request must not create a future deletion obligation for P2")
}

func allocationObserverFixture(t *testing.T, p *Provisioning, state dispatcherprivatev1.ProcessState) *ProcessObservations {
	t.Helper()
	mux := http.NewServeMux()
	path, handler := dispatcherprivatev1connect.NewExecutionServiceHandler(nativeProcessObservation{state: state})
	mux.Handle(path, handler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	observer, err := NewProcessObservations(p, NewDispatcherControl(server.Client(), server.URL, "dispatcher-secret"))
	require.NoError(t, err)
	return observer
}
