package execution

import (
	"context"
	"errors"
	"log/slog"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
)

// BatchSandboxes are the OpenSandbox custom resources that are the authority
// for environment lifecycle. Agent only reads them; creation and
// deletion go through the OpenSandbox API.
var BatchSandboxes = schema.GroupVersionResource{Group: "sandbox.opensandbox.io", Version: "v1alpha1", Resource: "batchsandboxes"}

// observationTimeout bounds one event's ownership lookup and wake.
const observationTimeout = 5 * time.Second

// SandboxObserver watches Talgent's sandboxes and wakes the allocation each one
// belongs to. It never decides or deletes: the allocation's reconciliation
// re-reads the sandbox and its exact ownership before acting. Watch
// interruptions and expired resource versions are recovered by relisting, which
// re-observes every current sandbox.
type SandboxObserver struct {
	client    dynamic.Interface
	namespace string
	recovery  *EnvironmentRecovery
	wake      func(context.Context, string) error
	logger    *slog.Logger
}

func NewSandboxObserver(client dynamic.Interface, namespace string, recovery *EnvironmentRecovery, wake func(context.Context, string) error, logger *slog.Logger) (*SandboxObserver, error) {
	if client == nil || namespace == "" || recovery == nil || wake == nil || logger == nil {
		return nil, errors.New("agent: sandbox observation needs a client, namespace, recovery and wake")
	}
	return &SandboxObserver{client: client, namespace: namespace, recovery: recovery, wake: wake, logger: logger}, nil
}

// Run observes sandboxes until ctx ends. Each observation is queued by the
// allocation label it carries; a failed lookup or wake is retried with backoff,
// because a retired allocation's late sandbox is woken by nothing else.
func (o *SandboxObserver) Run(ctx context.Context) error {
	labels := workqueue.NewTypedRateLimitingQueue(workqueue.NewTypedItemExponentialFailureRateLimiter[string](time.Second, time.Minute))
	factory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(o.client, 0, o.namespace, func(options *metav1.ListOptions) {
		options.LabelSelector = sandboxAllocationLabel
	})
	informer := factory.ForResource(BatchSandboxes).Informer()
	observe := func(object any) {
		if label := sandboxLabel(object); label != "" {
			labels.Add(label)
		}
	}
	if _, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: observe,
		UpdateFunc: func(previous, current any) {
			if sandboxVersion(previous) != sandboxVersion(current) {
				observe(current)
			}
		},
		DeleteFunc: observe,
	}); err != nil {
		labels.ShutDown()
		return err
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for o.next(ctx, labels) {
		}
	}()
	factory.Start(ctx.Done())
	<-ctx.Done()
	labels.ShutDown()
	factory.Shutdown()
	<-done
	return nil
}

// next wakes the allocation one queued label names.
func (o *SandboxObserver) next(ctx context.Context, labels workqueue.TypedRateLimitingInterface[string]) bool {
	label, shutdown := labels.Get()
	if shutdown {
		return false
	}
	defer labels.Done(label)
	attempt, cancel := context.WithTimeout(ctx, observationTimeout)
	defer cancel()
	allocation, found, err := o.recovery.AllocationForSandbox(attempt, label)
	if err == nil && found {
		err = o.wake(attempt, allocation)
	}
	switch {
	case err != nil:
		o.logger.WarnContext(attempt, "sandbox observation will be retried", slog.String("allocation_label", label), slog.Any("error", err))
		labels.AddRateLimited(label)
		return true
	case !found:
		o.logger.WarnContext(attempt, "sandbox ownership unknown; left in place", slog.String("namespace", o.namespace), slog.String("allocation_label", label))
	}
	labels.Forget(label)
	return true
}

func sandboxLabel(object any) string {
	if tombstone, ok := object.(cache.DeletedFinalStateUnknown); ok {
		object = tombstone.Obj
	}
	if sandbox, ok := object.(*unstructured.Unstructured); ok {
		return sandbox.GetLabels()[sandboxAllocationLabel]
	}
	return ""
}

func sandboxVersion(object any) string {
	if sandbox, ok := object.(*unstructured.Unstructured); ok {
		return sandbox.GetResourceVersion()
	}
	return ""
}
