package cluster_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/talgent-work/workbench/internal/business/computer/domain"
	"github.com/talgent-work/workbench/internal/business/computer/infrastructure/cluster"
)

func resources(cpu, memory, pods string) corev1.ResourceList {
	return corev1.ResourceList{corev1.ResourceRequestsCPU: resource.MustParse(cpu), corev1.ResourceRequestsMemory: resource.MustParse(memory), corev1.ResourcePods: resource.MustParse(pods)}
}

func quota(spec, status corev1.ResourceList) *corev1.ResourceQuota {
	return &corev1.ResourceQuota{ObjectMeta: metav1.ObjectMeta{Name: "executions", Namespace: "sandboxes"}, Spec: corev1.ResourceQuotaSpec{Hard: spec}, Status: corev1.ResourceQuotaStatus{Hard: status}}
}

func capacity(t *testing.T, client *fake.Clientset, demand corev1.ResourceList) (domain.Room, error) {
	t.Helper()
	reader, err := cluster.NewQuota(client.CoreV1().ResourceQuotas("sandboxes"), "executions", demand)
	require.NoError(t, err)
	return reader.Capacity(context.Background())
}

// The Cloud gets the quota's requested compute and as many runs as every
// dimension the quota limits fits, whatever the quota already counts used:
// the Cloud's Desks are what reserve room.
func TestCapacityIsTheRoomTheQuotaGivesRuns(t *testing.T) {
	gib := int64(1 << 30)
	for _, tc := range []struct {
		name       string
		hard       corev1.ResourceList
		demand     corev1.ResourceList
		wantDesks  int64
		wantCPU    int64
		wantMemory int64
	}{
		{"memory bounds the runs", resources("8", "12Gi", "64"), corev1.ResourceList{corev1.ResourceRequestsCPU: resource.MustParse("600m"), corev1.ResourceRequestsMemory: resource.MustParse("960Mi")}, 12, 8000, 12 * gib},
		{"pods bound the runs", resources("8", "12Gi", "3"), corev1.ResourceList{corev1.ResourceRequestsCPU: resource.MustParse("1"), corev1.ResourceRequestsMemory: resource.MustParse("1Gi")}, 3, 8000, 12 * gib},
		{"a limit bounds the runs", corev1.ResourceList{corev1.ResourceRequestsCPU: resource.MustParse("8"), corev1.ResourceRequestsMemory: resource.MustParse("12Gi"), corev1.ResourceLimitsCPU: resource.MustParse("10")},
			corev1.ResourceList{corev1.ResourceRequestsCPU: resource.MustParse("1"), corev1.ResourceRequestsMemory: resource.MustParse("1Gi"), corev1.ResourceLimitsCPU: resource.MustParse("4")}, 2, 8000, 12 * gib},
	} {
		t.Run(tc.name, func(t *testing.T) {
			used := quota(tc.hard, tc.hard)
			used.Status.Used = tc.hard.DeepCopy()
			client := fake.NewClientset(used)
			room, err := capacity(t, client, tc.demand)
			require.NoError(t, err)
			require.Equal(t, domain.Room{MilliCPU: tc.wantCPU, MemoryBytes: tc.wantMemory, Desks: tc.wantDesks}, room)
			require.Len(t, client.Actions(), 1, "only one read; no watch, write or queued request")
		})
	}
}

func TestCapacityFailsClosedUntilTheQuotaIsReconciled(t *testing.T) {
	demand := resources("1", "2Gi", "1")
	pending := quota(resources("4", "8Gi", "4"), nil)
	client := fake.NewClientset(pending)
	_, err := capacity(t, client, demand)
	require.ErrorIs(t, err, cluster.ErrQuotaNotReady)

	pending.Status.Hard = resources("4", "8Gi", "4")
	_, err = client.CoreV1().ResourceQuotas("sandboxes").UpdateStatus(context.Background(), pending, metav1.UpdateOptions{})
	require.NoError(t, err)
	room, err := capacity(t, client, demand)
	require.NoError(t, err)
	require.Equal(t, domain.Room{MilliCPU: 4000, MemoryBytes: 8 << 30, Desks: 4}, room)

	require.NoError(t, client.CoreV1().ResourceQuotas("sandboxes").Delete(context.Background(), "executions", metav1.DeleteOptions{}))
	_, err = capacity(t, client, demand)
	require.Error(t, err)
}

func TestCapacityRefusesAQuotaItCannotReadForEveryRun(t *testing.T) {
	scoped := quota(resources("4", "8Gi", "4"), resources("4", "8Gi", "4"))
	scoped.Spec.Scopes = []corev1.ResourceQuotaScope{corev1.ResourceQuotaScopeNotTerminating}
	_, err := capacity(t, fake.NewClientset(scoped), resources("1", "2Gi", "1"))
	require.ErrorIs(t, err, cluster.ErrQuotaNotReady)

	limited := resources("4", "8Gi", "4")
	limited[corev1.ResourceLimitsMemory] = resource.MustParse("16Gi")
	_, err = capacity(t, fake.NewClientset(quota(limited, limited)), resources("1", "2Gi", "1"))
	require.ErrorIs(t, err, cluster.ErrQuotaNotReady, "a limit the demand does not state")
}

// A quota change not yet reconciled gives the lower of the two bounds.
func TestCapacityUsesTheLowerBoundDuringAQuotaChange(t *testing.T) {
	client := fake.NewClientset(quota(resources("4", "8Gi", "4"), resources("2", "8Gi", "4")))
	room, err := capacity(t, client, resources("1", "2Gi", "1"))
	require.NoError(t, err)
	require.Equal(t, domain.Room{MilliCPU: 2000, MemoryBytes: 8 << 30, Desks: 2}, room)
}
