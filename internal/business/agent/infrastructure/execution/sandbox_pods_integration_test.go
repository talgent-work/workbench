//go:build integration

package execution

import (
	"context"
	"testing"
	"time"

	sdk "github.com/alibaba/OpenSandbox/sdks/sandbox/go"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"xorm.io/xorm"
)

// A Pod carrying the execution's ownership whose sandbox the server no longer
// shows is an ending environment: deleted, yet still able to write the Work's
// directories. A terminated Pod and another execution's Pod are not the
// execution's environments, and a Pod whose sandbox the list merely lags
// behind is that live sandbox.
func TestAPodOutlivingItsSandboxIsObservedAsEnding(t *testing.T) {
	ctx := context.Background()
	db, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	provider, cluster, fixture := sandboxClient(t)
	p := NewProvisioning(db, provider, fixtureSandboxCredentials(db, provider), NativeLaunchConfig{Binary: "claude"}, time.Now, nil)
	input := request()
	record, err := register(ctx, p, input, testMounts)
	require.NoError(t, err)
	owned := correlation(SandboxOwner{WorkID: input.WorkID, ID: record.ID})
	other := correlation(SandboxOwner{WorkID: input.WorkID, ID: "01900000-0000-7000-8000-000000000009"})
	pod := func(name, sandbox string, labels map[string]string, phase corev1.PodPhase) *corev1.Pod {
		withID := map[string]string{sandboxIDLabel: sandbox}
		for k, v := range labels {
			withID[k] = v
		}
		return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: fixtureSandboxNamespace, Labels: withID}, Status: corev1.PodStatus{Phase: phase}}
	}
	// A sandbox the list does not show yet, which a GET still finds.
	const unlisted = "22222222-2222-4222-8222-222222222222"
	fixture.others = map[string]sdk.SandboxInfo{unlisted: {ID: unlisted, Metadata: owned, Status: sdk.SandboxStatus{State: sdk.StateRunning}}}
	for _, created := range []*corev1.Pod{
		pod("ending-0", "sandbox-ending", owned, corev1.PodRunning),
		pod("unlisted-0", unlisted, owned, corev1.PodRunning),
		pod("done-0", "sandbox-done", owned, corev1.PodSucceeded),
		pod("foreign-0", "sandbox-foreign", other, corev1.PodRunning),
	} {
		_, err := cluster.CoreV1().Pods(fixtureSandboxNamespace).Create(ctx, created, metav1.CreateOptions{})
		require.NoError(t, err)
	}

	stored, found, err := p.records.get(ctx, record.ID)
	require.NoError(t, err)
	require.True(t, found)
	view, err := cloudEnvironment{sandboxes: provider}.observe(ctx, stored, false)
	require.NoError(t, err)
	require.ElementsMatch(t, []domain.ObservedEnvironment{{ID: "sandbox-ending", Ending: true}, {ID: unlisted, Running: true}}, view.environments, "a Pod whose sandbox still exists is that sandbox, not an ending one")
}
