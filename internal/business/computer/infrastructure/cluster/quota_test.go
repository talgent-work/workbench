package cluster_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/talgent-work/workbench/internal/business/computer/infrastructure/cluster"
)

func resources(cpu, memory, pods string) corev1.ResourceList {
	return corev1.ResourceList{corev1.ResourceRequestsCPU: resource.MustParse(cpu), corev1.ResourceRequestsMemory: resource.MustParse(memory), corev1.ResourcePods: resource.MustParse(pods)}
}
func TestQuotaPreflightRejectsExhaustionWithoutWaiting(t *testing.T) {
	for _, tc := range []struct {
		name, cpu, memory, pods string
		denied                  bool
	}{
		{"fits exactly", "3", "6Gi", "3", false},
		{"cpu full", "3500m", "6Gi", "3", true},
		{"memory full", "3", "7Gi", "3", true},
		{"pod count full", "3", "6Gi", "4", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			quota := &corev1.ResourceQuota{ObjectMeta: metav1.ObjectMeta{Name: "executions", Namespace: "sandboxes"}, Spec: corev1.ResourceQuotaSpec{Hard: resources("4", "8Gi", "4")}, Status: corev1.ResourceQuotaStatus{Hard: resources("4", "8Gi", "4"), Used: resources(tc.cpu, tc.memory, tc.pods)}}
			client := fake.NewClientset(quota)
			checker, err := cluster.NewQuota(client.CoreV1().ResourceQuotas("sandboxes"), "executions", resources("1", "2Gi", "1"))
			require.NoError(t, err)
			fits, err := checker.FitsRun(context.Background())
			require.NoError(t, err)
			require.Equal(t, !tc.denied, fits)
			require.Len(t, client.Actions(), 1, "only one snapshot read; no watch, write or queued request")
		})
	}
}
func TestQuotaPreflightFailsClosedUntilAccountingIsAvailable(t *testing.T) {
	quota := &corev1.ResourceQuota{ObjectMeta: metav1.ObjectMeta{Name: "executions", Namespace: "sandboxes"}, Spec: corev1.ResourceQuotaSpec{Hard: resources("4", "8Gi", "4")}}
	client := fake.NewClientset(quota)
	checker, err := cluster.NewQuota(client.CoreV1().ResourceQuotas("sandboxes"), "executions", resources("1", "2Gi", "1"))
	require.NoError(t, err)
	_, err = checker.FitsRun(context.Background())
	require.ErrorIs(t, err, cluster.ErrQuotaNotReady)
	quota.Status = corev1.ResourceQuotaStatus{Hard: resources("4", "8Gi", "4"), Used: resources("0", "0", "0")}
	_, err = client.CoreV1().ResourceQuotas("sandboxes").UpdateStatus(context.Background(), quota, metav1.UpdateOptions{})
	require.NoError(t, err)
	fits, err := checker.FitsRun(context.Background())
	require.NoError(t, err)
	require.True(t, fits)
	require.NoError(t, client.CoreV1().ResourceQuotas("sandboxes").Delete(context.Background(), "executions", metav1.DeleteOptions{}))
	_, err = checker.FitsRun(context.Background())
	require.Error(t, err)
}
func TestQuotaPreflightUsesLowerBoundDuringQuotaChange(t *testing.T) {
	quota := &corev1.ResourceQuota{ObjectMeta: metav1.ObjectMeta{Name: "executions", Namespace: "sandboxes"}, Spec: corev1.ResourceQuotaSpec{Hard: resources("4", "8Gi", "4")}, Status: corev1.ResourceQuotaStatus{Hard: resources("2", "8Gi", "4"), Used: resources("2", "0", "0")}}
	client := fake.NewClientset(quota)
	checker, err := cluster.NewQuota(client.CoreV1().ResourceQuotas("sandboxes"), "executions", resources("1", "2Gi", "1"))
	require.NoError(t, err)
	fits, err := checker.FitsRun(context.Background())
	require.NoError(t, err)
	require.False(t, fits)
}
