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
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
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
	p := NewProvisioning(db, provider, credentials, NativeLaunchConfig{Binary: "claude"}, time.Now, infrastructure.NewInitialExecutionRequests(db, time.Now), nil)
	input := request()
	allocation, err := register(ctx, p, input, testMounts)
	require.NoError(t, err)

	owned := correlation(Allocation{WorkID: input.WorkID, ID: allocation.ID})
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{BatchSandboxes: "BatchSandboxList"})
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

	orphan := batchSandbox("orphan", map[string]string{sandboxAllocationLabel: storageKey("unknown-allocation")[:48]})
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
	p := NewProvisioning(db, provider, credentials, NativeLaunchConfig{Binary: "claude"}, time.Now, infrastructure.NewInitialExecutionRequests(db, time.Now), nil)
	input := request()
	allocation, err := register(ctx, p, input, testMounts)
	require.NoError(t, err)
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{BatchSandboxes: "BatchSandboxList"})
	_, err = client.Resource(BatchSandboxes).Namespace("talgent").Create(ctx, batchSandbox("late", correlation(Allocation{WorkID: input.WorkID, ID: allocation.ID})), metav1.CreateOptions{})
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
