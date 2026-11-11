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
	"xorm.io/xorm"
)

// BatchSandboxes are the OpenSandbox custom resources that are the authority
// for environment lifecycle. Agent only reads them; creation and
// deletion go through the OpenSandbox API.
var BatchSandboxes = schema.GroupVersionResource{Group: "sandbox.opensandbox.io", Version: "v1alpha1", Resource: "batchsandboxes"}

// SandboxPodResources are the Pods each sandbox runs in, which outlive its
// BatchSandbox until they terminate; a Pod's exit wakes a release.
var SandboxPodResources = schema.GroupVersionResource{Version: "v1", Resource: "pods"}

// observationTimeout bounds one event's ownership lookup and wake.
const observationTimeout = 5 * time.Second

// SandboxObserver watches Talgent's sandboxes and their Pods and wakes the
// execution each one belongs to, so a Pod's exit wakes a release. It never decides or deletes: the execution's reconciliation
// re-reads the sandbox and its exact ownership before acting. Watch
// interruptions and expired resource versions are recovered by relisting, which
// re-observes every current sandbox.
type SandboxObserver struct {
	client    dynamic.Interface
	namespace string
	records   records
	wake      func(context.Context, string) error
	logger    *slog.Logger
	// retryBase and retryMax bound the delay before a failed observation is
	// retried, and retries how often it is; tests shorten them.
	retryBase, retryMax time.Duration
	retries             int
}

// observationRetries bounds the retries of one failed observation: with
// delays doubling from a second to a minute, about five minutes. One that
// exhausts them is logged as an error and dropped; the recovery sweep still
// wakes every unreleased execution once per interval.
const observationRetries = 10

func NewSandboxObserver(client dynamic.Interface, namespace string, db *xorm.Engine, wake func(context.Context, string) error, logger *slog.Logger) (*SandboxObserver, error) {
	if client == nil || namespace == "" || db == nil || wake == nil || logger == nil {
		return nil, errors.New("agent: sandbox observation needs a client, namespace, records and wake")
	}
	return &SandboxObserver{client: client, namespace: namespace, records: records{db: db}, wake: wake, logger: logger, retryBase: time.Second, retryMax: time.Minute, retries: observationRetries}, nil
}

// Run observes sandboxes and their Pods until ctx ends. Each observation is
// queued by the value of the ownership label it carries; a failed lookup or
// wake is retried with backoff, at most retries times, so a retired
// execution's late sandbox is woken before the next sweep.
func (o *SandboxObserver) Run(ctx context.Context) error {
	labels := workqueue.NewTypedRateLimitingQueue(workqueue.NewTypedItemExponentialFailureRateLimiter[string](o.retryBase, o.retryMax))
	factory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(o.client, 0, o.namespace, func(options *metav1.ListOptions) {
		options.LabelSelector = sandboxOwnershipLabel
	})
	observe := func(object any) {
		if label := sandboxLabel(object); label != "" {
			labels.Add(label)
		}
	}
	handler := cache.ResourceEventHandlerFuncs{
		AddFunc: observe,
		UpdateFunc: func(previous, current any) {
			if sandboxVersion(previous) != sandboxVersion(current) {
				observe(current)
			}
		},
		DeleteFunc: observe,
	}
	for _, resource := range []schema.GroupVersionResource{BatchSandboxes, SandboxPodResources} {
		if _, err := factory.ForResource(resource).Informer().AddEventHandler(handler); err != nil {
			labels.ShutDown()
			return err
		}
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

// next wakes the execution one queued label names.
func (o *SandboxObserver) next(ctx context.Context, labels workqueue.TypedRateLimitingInterface[string]) bool {
	label, shutdown := labels.Get()
	if shutdown {
		return false
	}
	defer labels.Done(label)
	attempt, cancel := context.WithTimeout(ctx, observationTimeout)
	defer cancel()
	execution, found, err := o.records.forSandbox(attempt, label)
	if err == nil && found {
		err = o.wake(attempt, execution)
	}
	switch {
	case err != nil && labels.NumRequeues(label) >= o.retries:
		o.logger.ErrorContext(attempt, "sandbox observation abandoned", slog.String("ownership_label", label), slog.Int("retries", o.retries), slog.Any("error", err))
	case err != nil:
		o.logger.WarnContext(attempt, "sandbox observation will be retried", slog.String("ownership_label", label), slog.Any("error", err))
		labels.AddRateLimited(label)
		return true
	case !found:
		o.logger.WarnContext(attempt, "sandbox ownership unknown; left in place", slog.String("namespace", o.namespace), slog.String("ownership_label", label))
	}
	labels.Forget(label)
	return true
}

func sandboxLabel(object any) string {
	if tombstone, ok := object.(cache.DeletedFinalStateUnknown); ok {
		object = tombstone.Obj
	}
	if sandbox, ok := object.(*unstructured.Unstructured); ok {
		return sandbox.GetLabels()[sandboxOwnershipLabel]
	}
	return ""
}

func sandboxVersion(object any) string {
	if sandbox, ok := object.(*unstructured.Unstructured); ok {
		return sandbox.GetResourceVersion()
	}
	return ""
}
