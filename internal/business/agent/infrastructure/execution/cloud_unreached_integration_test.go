//go:build integration

package execution

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	sdk "github.com/alibaba/OpenSandbox/sdks/sandbox/go"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"
	"xorm.io/xorm"
)

// A Cloud Work stopped during a Disconnection releases its sandbox although
// the execution never saw its process exit, once its Work reclaims the run
// beyond reach; a retirement for an exit that never came retires nothing.
func TestAnUnreachedCloudRunReleasesItsSandboxWithoutAnExit(t *testing.T) {
	ctx := t.Context()
	db, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	provider, _, lifecycle := sandboxClient(t)
	p := NewProvisioning(db, provider, fixtureSandboxCredentials(db, provider), NativeLaunchConfig{Binary: "claude"}, time.Now, infrastructure.NewInitialExecutionRequests(db, time.Now), nil)
	input := request()
	record, err := register(ctx, p, input, testMounts)
	require.NoError(t, err)
	connections, err := NewNativeConnections(p, p.credentials, http.DefaultClient, "http://dispatcher.invalid", "fixture", command.NewRecordRepositoryCheckoutsHandler(infrastructure.NewInitialExecutionRequests(db, time.Now)))
	require.NoError(t, err)
	steps := newExecutionSteps(t, p, connections, unread{}, nil, time.Now)
	run := current(t, steps, record.ID)
	require.True(t, run.RecordSubmission())
	run.Environment = sandboxID
	_, err = run.RecordLaunch("current-process")
	require.NoError(t, err)
	require.NoError(t, steps.executions.Save(ctx, run))
	var spec sdk.CreateSandboxRequest
	require.NoError(t, json.Unmarshal(record.SpecificationJSON, &spec))
	lifecycle.info = &sdk.SandboxInfo{ID: sandboxID, Metadata: spec.Metadata, Status: sdk.SandboxStatus{State: sdk.StateRunning}}

	require.NoError(t, steps.retire(ctx, input.WorkID, domain.RetiredByExit, time.Now()))
	_, live, err := p.live(ctx, input.WorkID)
	require.NoError(t, err)
	require.True(t, live, "a running process's sandbox waits for its exit")

	require.NoError(t, steps.retire(ctx, input.WorkID, domain.RetiredUnreached, time.Now()))
	_, live, err = p.live(ctx, input.WorkID)
	require.NoError(t, err)
	require.False(t, live, "the Work's reclamation beyond reach retires the execution")
	pending, err := unreleased(ctx, db)
	require.NoError(t, err)
	require.Len(t, pending, 1, "the sandbox is released, its confirmation completing the stop")
	require.NoError(t, steps.retire(ctx, input.WorkID, domain.RetiredUnreached, time.Now()), "a repeated request finds the retired execution")
	require.NoError(t, steps.reconcile(ctx, record.ID))
	require.Nil(t, lifecycle.info, "the sandbox is deleted")
	require.Equal(t, domain.ExecutionReleased, current(t, steps, record.ID).State)
}
