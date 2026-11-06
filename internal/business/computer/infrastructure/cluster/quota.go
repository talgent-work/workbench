// Package cluster supplies a Cloud's admission with whether its cluster's
// quota fits one more run.
package cluster

import (
	"context"
	"errors"
	"fmt"

	"github.com/samber/oops"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"

	"github.com/talgent-work/workbench/internal/business/computer/application"
)

var ErrQuotaNotReady = errors.New("computer: execution resource quota is not ready")

// Quota checks a namespace-wide compute quota against one run's complete
// resource demand, including sidecars and runtime overhead. This is a
// preflight snapshot, not a reservation: the Cloud's Desks reserve room, and
// Kubernetes still enforces the quota when compute is created, since services
// share it.
type Quota struct {
	quotas typedcorev1.ResourceQuotaInterface
	name   string
	demand corev1.ResourceList
}

var _ application.ClusterQuota = (*Quota)(nil)

func NewQuota(quotas typedcorev1.ResourceQuotaInterface, name string, demand corev1.ResourceList) (*Quota, error) {
	if quotas == nil || name == "" {
		return nil, errors.New("computer: quota client and name are required")
	}
	for _, key := range []corev1.ResourceName{corev1.ResourceRequestsCPU, corev1.ResourceRequestsMemory} {
		value, ok := demand[key]
		if !ok || value.Sign() <= 0 {
			return nil, fmt.Errorf("computer: positive execution demand for %s is required", key)
		}
	}
	copied := demand.DeepCopy()
	copied[corev1.ResourcePods] = resource.MustParse("1")
	for key, value := range copied {
		if value.Sign() < 0 {
			return nil, fmt.Errorf("computer: negative execution demand for %s", key)
		}
	}
	return &Quota{quotas: quotas, name: name, demand: copied}, nil
}

// FitsRun reports whether the quota fits one more run now.
func (c *Quota) FitsRun(ctx context.Context) (bool, error) {
	quota, err := c.quotas.Get(ctx, c.name, metav1.GetOptions{})
	if err != nil {
		return false, oops.With("quota", c.name).Wrap(err)
	}
	// Without the Pod's scope predicates a scoped quota is not authoritative for
	// this request. Runtime must supply the namespace-wide execution quota.
	if len(quota.Spec.Scopes) > 0 || quota.Spec.ScopeSelector != nil {
		return false, ErrQuotaNotReady
	}
	for _, key := range []corev1.ResourceName{corev1.ResourceRequestsCPU, corev1.ResourceRequestsMemory} {
		if _, ok := quota.Spec.Hard[key]; !ok {
			return false, ErrQuotaNotReady
		}
	}
	for key, required := range c.demand {
		hard, limited := quota.Spec.Hard[key]
		if !limited {
			continue
		}
		observedHard, ready := quota.Status.Hard[key]
		used, accounted := quota.Status.Used[key]
		if !ready || !accounted {
			return false, ErrQuotaNotReady
		}
		// A quota change may not yet be reconciled. Honor whichever bound is lower.
		if observedHard.Cmp(hard) < 0 {
			hard = observedHard
		}
		used.Add(required)
		if used.Cmp(hard) > 0 {
			return false, nil
		}
	}
	// A limit not represented in the complete demand cannot be checked safely.
	for _, key := range []corev1.ResourceName{corev1.ResourceLimitsCPU, corev1.ResourceLimitsMemory} {
		if _, limited := quota.Spec.Hard[key]; limited {
			if _, known := c.demand[key]; !known {
				return false, ErrQuotaNotReady
			}
		}
	}
	return true, nil
}
