//go:build integration

package execution

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"testing"
	"time"

	sdk "github.com/alibaba/OpenSandbox/sdks/sandbox/go"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"xorm.io/xorm"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// A deployment upgrades with two executions its previous controlplane left:
// one retired whose sandbox's release is outstanding, and one whose creation
// it submitted but whose sandbox it never bound. Both sandboxes carry the
// previous ownership label. After the migration the upgraded controlplane
// reconciles them in the AgentExecution names: the observer wakes the
// execution a previously labelled sandbox belongs to, the submitted
// execution binds its sandbox and renews its lease, and both release their
// sandboxes once retired.
func TestAnUpgradedDeploymentConvergesThroughReconciliation(t *testing.T) {
	ctx := context.Background()
	at := time.Now()
	now := func() time.Time { return at }
	u := newUpgrade(t)
	provider, _, lifecycle := sandboxClient(t)
	lifecycle.clock = now

	// The previous controlplane's executions, recorded in the current schema
	// and then written into the previous one as it recorded them.
	before, err := xorm.NewEngine("mysql", u.dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = before.Close() })
	previous := NewProvisioning(before, provider, fixtureSandboxCredentials(before, provider), NativeLaunchConfig{Binary: "claude"}, now, nil)
	executions := NewAgentExecutionRepository(before, now)
	retired, err := register(ctx, previous, request(), testMounts)
	require.NoError(t, err)
	run, err := executions.Get(ctx, retired.ID)
	require.NoError(t, err)
	require.True(t, run.RecordSubmission())
	_, err = run.RecordCreationAnswer(domain.CreationAnswer{EnvironmentID: surplusSandboxID})
	require.NoError(t, err)
	_, err = run.RecordLaunch("process-0")
	require.NoError(t, err)
	run.RecordExit("process-0", domain.HarnessExit{})
	changed, err := run.Retire(domain.WorkComputeReleaseRequested{WorkID: run.WorkID, StartAdmittedAt: run.StartAdmittedAt, Cause: domain.RetiredByExit}, at)
	require.NoError(t, err)
	require.True(t, changed)
	require.NoError(t, executions.Save(ctx, run))
	submitted, err := register(ctx, previous, request(), testMounts)
	require.NoError(t, err)
	run, err = executions.Get(ctx, submitted.ID)
	require.NoError(t, err)
	require.True(t, run.RecordSubmission())
	require.NoError(t, executions.Save(ctx, run))

	retiredLabels, submittedLabels := u.seed(t, retired.ID), u.seed(t, submitted.ID)
	expiresAt := at.Add(30 * time.Minute).UTC()
	lifecycle.mu.Lock()
	lifecycle.info = &sdk.SandboxInfo{ID: sandboxID, Metadata: submittedLabels, Status: sdk.SandboxStatus{State: sdk.StateRunning}, ExpiresAt: &expiresAt}
	lifecycle.others = map[string]sdk.SandboxInfo{surplusSandboxID: {ID: surplusSandboxID, Metadata: retiredLabels, Status: sdk.SandboxStatus{State: sdk.StateRunning}}}
	lifecycle.mu.Unlock()

	u.migrate(t)
	u.requireCurrentSchema(t)

	db := u.engine(t)
	p := NewProvisioning(db, provider, fixtureSandboxCredentials(db, provider), NativeLaunchConfig{Binary: "claude"}, now, nil)
	attachNativeCredentials(t, db, p, lifecycle)
	connections, err := NewNativeConnections(p, p.credentials, http.DefaultClient, "http://dispatcher.invalid", "fixture")
	require.NoError(t, err)
	steps := newExecutionSteps(t, p, connections, unread{}, nil, now)

	require.Equal(t, []string{submitted.ID}, wokenBy(t, db, batchSandbox("previous", submittedLabels)), "a previously labelled sandbox wakes its execution")

	_, err = steps.handler.Reconcile(ctx, submitted.ID)
	require.NoError(t, err)
	run = current(t, steps, submitted.ID)
	require.Equal(t, sandboxID, run.Environment, "the execution binds the sandbox its previous controlplane created")
	lifecycle.mu.Lock()
	renewals := append([]time.Time(nil), lifecycle.renewals...)
	lifecycle.mu.Unlock()
	require.Len(t, renewals, 1, "the bound sandbox's lease, below its renewal threshold, is renewed")
	require.Equal(t, at.Add(fixtureLease.Duration).Truncate(time.Millisecond).UTC(), run.Lease.UTC(), "the renewed expiry is recorded")

	_, err = run.RecordLaunch("process-1")
	require.NoError(t, err)
	run.RecordExit("process-1", domain.HarnessExit{})
	require.NoError(t, steps.executions.Save(ctx, run))
	require.NoError(t, steps.retire(ctx, run.WorkID, domain.RetiredByExit, at))
	for _, id := range []string{retired.ID, submitted.ID} {
		require.NoError(t, steps.reconcile(ctx, id))
		require.Equal(t, domain.ExecutionReleased, current(t, steps, id).State, "the retired execution is released")
	}
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	require.Empty(t, lifecycle.listed(""), "both previously labelled sandboxes are deleted")
	require.Equal(t, 2, lifecycle.deletes)
}

// wokenBy runs the sandbox observer over one observed sandbox and returns the
// executions it woke.
func wokenBy(t *testing.T, db *xorm.Engine, sandbox *unstructured.Unstructured) []string {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{BatchSandboxes: "BatchSandboxList"})
	_, err := client.Resource(BatchSandboxes).Namespace("talgent").Create(ctx, sandbox, metav1.CreateOptions{})
	require.NoError(t, err)
	var mu sync.Mutex
	var woken []string
	observer, err := NewSandboxObserver(client, "talgent", db, func(_ context.Context, id string) error {
		mu.Lock()
		defer mu.Unlock()
		woken = append(woken, id)
		return nil
	}, slog.Default())
	require.NoError(t, err)
	done := make(chan struct{})
	go func() { defer close(done); _ = observer.Run(ctx) }()
	require.Eventually(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(woken) > 0 }, 5*time.Second, 10*time.Millisecond)
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	return append([]string(nil), woken...)
}
