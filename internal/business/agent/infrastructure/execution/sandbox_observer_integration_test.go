//go:build integration

package execution

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
	"xorm.io/xorm"
)

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}
func (s *syncBuffer) String() string { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

// observedKinds are the list kinds of the resources the observer watches.
var observedKinds = map[schema.GroupVersionResource]string{BatchSandboxes: "BatchSandboxList", SandboxPodResources: "PodList"}

func sandboxPod(name string, labels map[string]string) *unstructured.Unstructured {
	pod := &unstructured.Unstructured{}
	pod.SetAPIVersion("v1")
	pod.SetKind("Pod")
	pod.SetNamespace("talgent")
	pod.SetName(name)
	pod.SetLabels(labels)
	return pod
}

func batchSandbox(name string, labels map[string]string) *unstructured.Unstructured {
	sandbox := &unstructured.Unstructured{}
	sandbox.SetAPIVersion("sandbox.opensandbox.io/v1alpha1")
	sandbox.SetKind("BatchSandbox")
	sandbox.SetNamespace("talgent")
	sandbox.SetName(name)
	sandbox.SetLabels(labels)
	return sandbox
}

// Every observed Talgent sandbox wakes exactly the allocation its label names,
// whether it already existed, appears late or disappears. A sandbox whose label
// names no allocation is reported and never acted on.
func TestSandboxObserverWakesTheOwningAllocation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	provider, _, _ := sandboxClient(t)
	credentials := fixtureSandboxCredentials(db, provider)
	p := NewProvisioning(db, provider, credentials, NativeLaunchConfig{Binary: "claude"}, time.Now, nil)
	input := request()
	allocation, err := register(ctx, p, input, testMounts)
	require.NoError(t, err)

	owned := correlation(SandboxOwner{WorkID: input.WorkID, ID: allocation.ID})
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), observedKinds)
	_, err = client.Resource(BatchSandboxes).Namespace("talgent").Create(ctx, batchSandbox("existing", owned), metav1.CreateOptions{})
	require.NoError(t, err)
	var mu sync.Mutex
	var woken []string
	wake := func(_ context.Context, id string) error {
		mu.Lock()
		defer mu.Unlock()
		woken = append(woken, id)
		return nil
	}
	wakes := func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), woken...) }
	logs := &syncBuffer{}
	observer, err := NewSandboxObserver(client, "talgent", db, wake, slog.New(slog.NewTextHandler(logs, nil)))
	require.NoError(t, err)
	go func() { _ = observer.Run(ctx) }()

	require.Eventually(t, func() bool { return len(wakes()) == 1 }, 5*time.Second, 10*time.Millisecond, "the initial list observes existing sandboxes")
	require.Equal(t, allocation.ID, wakes()[0])

	late := batchSandbox("late", owned)
	_, err = client.Resource(BatchSandboxes).Namespace("talgent").Create(ctx, late, metav1.CreateOptions{})
	require.NoError(t, err)
	require.Eventually(t, func() bool { return len(wakes()) == 2 }, 5*time.Second, 10*time.Millisecond, "a late sandbox wakes its allocation")

	require.NoError(t, client.Resource(BatchSandboxes).Namespace("talgent").Delete(ctx, "existing", metav1.DeleteOptions{}))
	require.Eventually(t, func() bool { return len(wakes()) == 3 }, 5*time.Second, 10*time.Millisecond, "a disappeared sandbox wakes its allocation")

	orphan := batchSandbox("orphan", map[string]string{sandboxSessionLabel: storageKey(input.WorkID)[:48], sandboxOwnershipLabel: storageKey("unknown-execution")[:48]})
	_, err = client.Resource(BatchSandboxes).Namespace("talgent").Create(ctx, orphan, metav1.CreateOptions{})
	require.NoError(t, err)
	require.Eventually(t, func() bool { return bytes.Contains([]byte(logs.String()), []byte("sandbox ownership unknown")) }, 5*time.Second, 10*time.Millisecond)
	require.Len(t, wakes(), 3, "a sandbox with no allocation wakes nothing")
	for _, id := range wakes() {
		require.Equal(t, allocation.ID, id)
	}
}

// A failed lookup or wake is retried: a retired allocation's late sandbox is
// woken by nothing else.
func TestSandboxObservationRetriesAFailedWake(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	provider, _, _ := sandboxClient(t)
	credentials := fixtureSandboxCredentials(db, provider)
	p := NewProvisioning(db, provider, credentials, NativeLaunchConfig{Binary: "claude"}, time.Now, nil)
	input := request()
	allocation, err := register(ctx, p, input, testMounts)
	require.NoError(t, err)
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), observedKinds)
	_, err = client.Resource(BatchSandboxes).Namespace("talgent").Create(ctx, batchSandbox("late", correlation(SandboxOwner{WorkID: input.WorkID, ID: allocation.ID})), metav1.CreateOptions{})
	require.NoError(t, err)
	var mu sync.Mutex
	attempts := 0
	wake := func(context.Context, string) error {
		mu.Lock()
		defer mu.Unlock()
		attempts++
		if attempts == 1 {
			return errors.New("redis unavailable")
		}
		return nil
	}
	observer, err := NewSandboxObserver(client, "talgent", db, wake, slog.Default())
	require.NoError(t, err)
	go func() { _ = observer.Run(ctx) }()
	require.Eventually(t, func() bool { mu.Lock(); defer mu.Unlock(); return attempts == 2 }, 10*time.Second, 20*time.Millisecond)
}

// A wake that keeps failing is retried up to its ceiling, then given up with
// an error naming the sandbox's ownership; the recovery sweep still wakes
// every unreleased execution.
func TestSandboxObservationGivesUpAWakeAtItsCeiling(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	provider, _, _ := sandboxClient(t)
	p := NewProvisioning(db, provider, fixtureSandboxCredentials(db, provider), NativeLaunchConfig{Binary: "claude"}, time.Now, nil)
	input := request()
	allocation, err := register(ctx, p, input, testMounts)
	require.NoError(t, err)
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), observedKinds)
	_, err = client.Resource(BatchSandboxes).Namespace("talgent").Create(ctx, batchSandbox("late", correlation(SandboxOwner{WorkID: input.WorkID, ID: allocation.ID})), metav1.CreateOptions{})
	require.NoError(t, err)
	var mu sync.Mutex
	attempts := 0
	wake := func(context.Context, string) error {
		mu.Lock()
		defer mu.Unlock()
		attempts++
		return errors.New("redis unavailable")
	}
	logs := &syncBuffer{}
	observer, err := NewSandboxObserver(client, "talgent", db, wake, slog.New(slog.NewJSONHandler(logs, nil)))
	require.NoError(t, err)
	observer.retryBase, observer.retryMax, observer.retries = time.Millisecond, 5*time.Millisecond, 3
	go func() { _ = observer.Run(ctx) }()
	require.Eventually(t, func() bool { return bytes.Contains([]byte(logs.String()), []byte("sandbox observation abandoned")) }, 2*time.Second, 10*time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, 4, attempts, "one attempt and three retries, no more")
	require.Contains(t, logs.String(), `"level":"ERROR"`)
	require.Contains(t, logs.String(), `"error":"redis unavailable"`)
}

// Only talgent.io/execution marks a sandbox's ownership: the observer selects
// sandboxes by it, and a sandbox without it wakes nothing, even when it
// carries the session label and another label naming the execution.
func TestSandboxObserverSelectsByTheOwnershipLabel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	provider, _, _ := sandboxClient(t)
	p := NewProvisioning(db, provider, fixtureSandboxCredentials(db, provider), NativeLaunchConfig{Binary: "claude"}, time.Now, nil)
	input := request()
	execution, err := register(ctx, p, input, testMounts)
	require.NoError(t, err)
	owned := correlation(SandboxOwner{WorkID: input.WorkID, ID: execution.ID})
	unlabelled := map[string]string{sandboxSessionLabel: owned[sandboxSessionLabel], "talgent.io/allocation": owned["talgent.io/execution"]}

	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), observedKinds)
	var mu sync.Mutex
	var selectors []string
	client.PrependReactor("list", "batchsandboxes", func(action k8stesting.Action) (bool, runtime.Object, error) {
		mu.Lock()
		defer mu.Unlock()
		selectors = append(selectors, action.(k8stesting.ListAction).GetListRestrictions().Labels.String())
		return false, nil, nil
	})
	// The informer lists, then watches; a sandbox created between the two never
	// reaches the fake's watch. Open the watch here so the test knows when it is.
	watching := make(chan struct{})
	var watchOnce sync.Once
	client.PrependWatchReactor("batchsandboxes", func(action k8stesting.Action) (bool, watch.Interface, error) {
		w, err := client.Tracker().Watch(action.GetResource(), action.GetNamespace())
		watchOnce.Do(func() { close(watching) })
		return true, w, err
	})
	_, err = client.Resource(BatchSandboxes).Namespace("talgent").Create(ctx, batchSandbox("unlabelled", unlabelled), metav1.CreateOptions{})
	require.NoError(t, err)
	var woken []string
	wake := func(_ context.Context, id string) error {
		mu.Lock()
		defer mu.Unlock()
		woken = append(woken, id)
		return nil
	}
	observer, err := NewSandboxObserver(client, "talgent", db, wake, slog.Default())
	require.NoError(t, err)
	go func() { _ = observer.Run(ctx) }()

	select {
	case <-watching:
	case <-time.After(5 * time.Second):
		t.Fatal("the observer never watched batchsandboxes")
	}
	_, err = client.Resource(BatchSandboxes).Namespace("talgent").Create(ctx, batchSandbox("late-unlabelled", unlabelled), metav1.CreateOptions{})
	require.NoError(t, err)
	_, err = client.Resource(BatchSandboxes).Namespace("talgent").Create(ctx, batchSandbox("owned", owned), metav1.CreateOptions{})
	require.NoError(t, err)
	require.Eventually(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(woken) > 0 }, 5*time.Second, 10*time.Millisecond)
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, []string{execution.ID}, woken, "only the sandbox carrying talgent.io/execution wakes its execution")
	require.Equal(t, "talgent.io/execution", selectors[0], "the observer selects by the ownership label")
}

// A sandbox's Pod outlives its BatchSandbox until it terminates, so the Pod's
// exit wakes the execution waiting for its release (Release waits for the
// Pod).
func TestSandboxObserverWakesTheOwningExecutionWhenItsPodExits(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	provider, _, _ := sandboxClient(t)
	p := NewProvisioning(db, provider, fixtureSandboxCredentials(db, provider), NativeLaunchConfig{Binary: "claude"}, time.Now, nil)
	input := request()
	execution, err := register(ctx, p, input, testMounts)
	require.NoError(t, err)
	owned := correlation(SandboxOwner{WorkID: input.WorkID, ID: execution.ID})
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), observedKinds)
	_, err = client.Resource(SandboxPodResources).Namespace("talgent").Create(ctx, sandboxPod("sandbox-0", owned), metav1.CreateOptions{})
	require.NoError(t, err)
	var mu sync.Mutex
	var woken []string
	wake := func(_ context.Context, id string) error {
		mu.Lock()
		defer mu.Unlock()
		woken = append(woken, id)
		return nil
	}
	wakes := func() int { mu.Lock(); defer mu.Unlock(); return len(woken) }
	observer, err := NewSandboxObserver(client, "talgent", db, wake, slog.Default())
	require.NoError(t, err)
	go func() { _ = observer.Run(ctx) }()
	require.Eventually(t, func() bool { return wakes() == 1 }, 5*time.Second, 10*time.Millisecond, "an existing Pod is observed")

	require.NoError(t, client.Resource(SandboxPodResources).Namespace("talgent").Delete(ctx, "sandbox-0", metav1.DeleteOptions{}))
	require.Eventually(t, func() bool { return wakes() == 2 }, 5*time.Second, 10*time.Millisecond, "the Pod's exit wakes its execution")
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, []string{execution.ID, execution.ID}, woken)
}
