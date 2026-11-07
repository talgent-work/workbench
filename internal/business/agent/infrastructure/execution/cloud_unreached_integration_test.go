//go:build integration

package execution

import (
	"context"
	"testing"
	"time"

	sdk "github.com/alibaba/OpenSandbox/sdks/sandbox/go"
	"github.com/stretchr/testify/require"
	dispatcherprivatev1 "github.com/talgent-work/contracts/gen/dispatcher/private/v1"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"
	"xorm.io/xorm"
)

// unreachedWorks holds a Stopping Cloud Work whose process never exited,
// its run reclaimed beyond reach once the test says so.
type unreachedWorks struct{ unreached bool }

func (w *unreachedWorks) Get(_ context.Context, id string) (*domain.AgentWork, error) {
	return &domain.AgentWork{ID: id, State: domain.WorkStopping, StopRequestReference: "stop", HarnessProcess: &domain.HarnessProcess{Unreached: w.unreached}}, nil
}

func (*unreachedWorks) Save(context.Context, *domain.AgentWork) error { return nil }

// A Cloud Work stopped during a Disconnection reclaims its sandbox although
// the allocation never saw its process exit; a reclamation request its Work
// did not make that way still waits for the exit.
func TestAnUnreachedCloudRunReleasesItsSandboxWithoutAnExit(t *testing.T) {
	db, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	sandboxes, _, lifecycle := sandboxClient(t)
	works := &unreachedWorks{}
	p := NewProvisioning(db, sandboxes, &SandboxCredentials{works: NewHeldWorks(db, works)}, NativeLaunchConfig{}, time.Now, nil, nil)
	current := allocationRecord{ID: "2b5cff9e-e3a6-4e56-9a2c-5aa9a8a7e016", SessionID: "work", SandboxID: sandboxID, ProcessID: "current-process", CreatedAt: time.Now().UnixMilli(), RequestJSON: []byte(`{}`), CredentialPlanJSON: []byte(`{}`), ServiceAccessJSON: []byte(`[]`), SpecificationJSON: []byte(`{}`)}
	_, err = db.Insert(&current)
	require.NoError(t, err)
	lifecycle.info = &sdk.SandboxInfo{ID: sandboxID, Metadata: correlation(Allocation{ID: current.ID, WorkID: "work"})}
	removals := NewRemovals(db, time.Now)
	environments, err := NewEnvironments(p, removals, allocationObserverFixture(t, p, dispatcherprivatev1.ProcessState_PROCESS_STATE_RUNNING))
	require.NoError(t, err)

	require.NoError(t, environments.Delete(t.Context(), "work"))
	_, live, err := p.live(t.Context(), "work")
	require.NoError(t, err)
	require.True(t, live, "a running process's sandbox waits for its exit")

	works.unreached = true
	require.NoError(t, environments.Delete(t.Context(), "work"))
	_, live, err = p.live(t.Context(), "work")
	require.NoError(t, err)
	require.False(t, live, "the Work's reclamation beyond reach retires the allocation")
	pending, err := pendingRemovals(t.Context(), removals)
	require.NoError(t, err)
	require.Len(t, pending, 1, "the sandbox is released, its confirmation completing the stop")
	require.NoError(t, environments.Delete(t.Context(), "work"), "a repeated request finds the retained removal")
}
