// Package cluster supplies a Cloud's Capacity from its cluster's execution
// quota.
package cluster

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/samber/oops"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"

	"github.com/talgent-work/workbench/internal/business/computer/application"
	"github.com/talgent-work/workbench/internal/business/computer/domain"
)

var ErrQuotaNotReady = errors.New("computer: execution resource quota is not ready")

// Quota reads a Cloud's Capacity from the namespace-wide quota that holds its
// runs, the one place the platform sets it: the quota's requested compute,
// and how many runs every dimension it limits fits, sidecars and runtime
// overhead included. The Cloud's Desks reserve within it, since the quota's
// own usage lags runs admitted but not yet created; Kubernetes still enforces
// it when compute is created.
type Quota struct {
	quotas typedcorev1.ResourceQuotaInterface
	name   string
	demand corev1.ResourceList
}

var _ application.CloudCapacity = (*Quota)(nil)

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

// Capacity is the room the quota gives runs now.
func (c *Quota) Capacity(ctx context.Context) (domain.Room, error) {
	quota, err := c.quotas.Get(ctx, c.name, metav1.GetOptions{})
	if err != nil {
		return domain.Room{}, oops.With("quota", c.name).Wrap(err)
	}
	// Without the Pod's scope predicates a scoped quota is not authoritative for
	// this request. Runtime must supply the namespace-wide execution quota.
	if len(quota.Spec.Scopes) > 0 || quota.Spec.ScopeSelector != nil {
		return domain.Room{}, ErrQuotaNotReady
	}
	for _, key := range []corev1.ResourceName{corev1.ResourceRequestsCPU, corev1.ResourceRequestsMemory} {
		if _, ok := quota.Spec.Hard[key]; !ok {
			return domain.Room{}, ErrQuotaNotReady
		}
	}
	// A limit not represented in the complete demand cannot be checked safely.
	for _, key := range []corev1.ResourceName{corev1.ResourceLimitsCPU, corev1.ResourceLimitsMemory} {
		if _, limited := quota.Spec.Hard[key]; limited {
			if _, known := c.demand[key]; !known {
				return domain.Room{}, ErrQuotaNotReady
			}
		}
	}
	hard := make(corev1.ResourceList, len(c.demand))
	runs := int64(math.MaxInt64)
	for key, required := range c.demand {
		bound, limited := quota.Spec.Hard[key]
		if !limited {
			continue
		}
		// Kubernetes admits against the hard limits its quota controller has
		// reconciled; a change not yet reconciled honors the lower bound.
		observed, ready := quota.Status.Hard[key]
		if !ready {
			return domain.Room{}, ErrQuotaNotReady
		}
		if observed.Cmp(bound) < 0 {
			bound = observed
		}
		hard[key] = bound
		if required.Sign() > 0 {
			runs = min(runs, bound.MilliValue()/required.MilliValue())
		}
	}
	cpu, memory := hard[corev1.ResourceRequestsCPU], hard[corev1.ResourceRequestsMemory]
	return domain.Room{MilliCPU: cpu.MilliValue(), MemoryBytes: memory.Value(), Desks: max(runs, 0)}, nil
}
