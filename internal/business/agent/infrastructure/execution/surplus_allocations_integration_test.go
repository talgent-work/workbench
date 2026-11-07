//go:build integration

package execution

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"

	dispatcherprivatev1 "github.com/talgent-work/contracts/gen/go/dispatcher/private/v1"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"

	sdk "github.com/alibaba/OpenSandbox/sdks/sandbox/go"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	shared "github.com/talgent-work/workbench/internal/pkg/opensandbox"
	"xorm.io/xorm"
)

// The real SQL allocation CAS and lifecycle SDK carry this recovery risk. The
// provider fixture controls duplicate/late visibility and asynchronous deletion.
func TestDuplicateAndLateEnvironmentsRetainDurablePrimaryAndAllocationIsolation(t *testing.T) {
	ctx := context.Background()
	dsn := mysqltest.Database(t)
	db, err := xorm.NewEngine("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	vault := &vaultFixture{}
	egress := httptest.NewServer(vault)
	defer egress.Close()
	var mu sync.Mutex
	objects := map[string]sdk.SandboxInfo{}
	var deletes []string
	var creates int
	var holdDeletion bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/sandboxes" {
			if r.Method == http.MethodPost {
				creates++
				w.WriteHeader(500)
				return
			}
			filter, _ := url.ParseQuery(r.URL.Query().Get("metadata"))
			items := []sdk.SandboxInfo{}
			for _, item := range objects {
				matches := true
				for key, value := range filter {
					matches = matches && item.Metadata[key] == value[0]
				}
				if matches {
					items = append(items, item)
				}
			}
			_ = json.NewEncoder(w).Encode(sdk.ListSandboxesResponse{Items: items})
			return
		}
		if strings.HasSuffix(r.URL.Path, fmt.Sprintf("/endpoints/%d", CredentialManagementPort)) {
			_ = json.NewEncoder(w).Encode(sdk.Endpoint{Endpoint: egress.URL, Headers: map[string]string{"OPENSANDBOX-EGRESS-AUTH": "egress-secret"}})
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/v1/sandboxes/")
		item, exists := objects[id]
		if !exists {
			w.WriteHeader(404)
			return
		}
		if r.Method == http.MethodDelete {
			deletes = append(deletes, id)
			if !holdDeletion {
				delete(objects, id)
			}
			w.WriteHeader(204)
			return
		}
		_ = json.NewEncoder(w).Encode(item)
	}))
	defer server.Close()
	client, err := shared.NewLifecycleClient(server.Client(), shared.Config{Address: server.URL + "/v1", APIKey: "fixture"})
	require.NoError(t, err)
	provider, err := NewSandboxes(client, config())
	require.NoError(t, err)
	credentials := fixtureSandboxCredentials(db, provider)
	newWorker := func() *Provisioning {
		return NewProvisioning(db, provider, fixtureSandboxCredentials(db, provider), NativeLaunchConfig{Binary: "claude"}, time.Now, infrastructure.NewInitialExecutionRequests(db, time.Now), nil)
	}
	p := newWorker()
	input := request()
	require.NoError(t, p.Register(ctx, input, testMounts))
	record, _, err := latestAllocation(ctx, p.db, input.WorkID)
	require.NoError(t, err)
	allocation := Allocation{WorkID: input.WorkID, ID: record.ID}
	require.NoError(t, p.update(ctx, record, "submitted = 1"))
	var spec sdk.CreateSandboxRequest
	require.NoError(t, json.Unmarshal(record.SpecificationJSON, &spec))
	first, second := uuid.NewString(), uuid.NewString()
	mu.Lock()
	objects[first] = sdk.SandboxInfo{ID: first, Metadata: spec.Metadata}
	objects[second] = sdk.SandboxInfo{ID: second, Metadata: spec.Metadata}
	mu.Unlock()
	var workers sync.WaitGroup
	for range 2 {
		workers.Add(1)
		go func() { defer workers.Done(); _ = submitPending(ctx, newWorker()) }()
	}
	workers.Wait()
	record, _, err = latestAllocation(ctx, p.db, input.WorkID)
	require.NoError(t, err)
	require.Contains(t, []string{first, second}, record.SandboxID)
	primary := record.SandboxID
	surplus := first
	if surplus == primary {
		surplus = second
	}
	mu.Lock()
	require.Empty(t, deletes, "no cleanup before a primary is durably bound")
	require.Zero(t, creates, "duplicate discovery never authorizes a POST")
	holdDeletion = true
	mu.Unlock()
	require.NoError(t, provider.prune(ctx, allocation, spec, primary, credentials))
	mu.Lock()
	require.Contains(t, objects, surplus, "DELETE acceptance can leave the environment present")
	require.Equal(t, []string{surplus}, deletes)
	holdDeletion = false
	mu.Unlock()
	require.NoError(t, provider.prune(ctx, allocation, spec, primary, credentials))
	mu.Lock()
	require.Contains(t, objects, primary)
	require.NotContains(t, objects, surplus)
	mu.Unlock()
	removals := NewRemovals(db, time.Now)
	root, err := os.OpenRoot(t.TempDir())
	require.NoError(t, err)
	defer root.Close()
	environments, err := NewEnvironments(p, removals, allocationObserverFixture(t, p, dispatcherprivatev1.ProcessState_PROCESS_STATE_RUNNING))
	require.NoError(t, err)
	_, err = db.Exec("UPDATE execution_allocation SET process_id = 'current-process', process_exited = 1 WHERE session_id = ? AND deleted_at = 0", input.WorkID)
	require.NoError(t, err)
	require.NoError(t, environments.Delete(ctx, input.WorkID))
	beforeWatch, _, err := latestAllocation(ctx, p.db, input.WorkID)
	require.NoError(t, err)
	watcher := newWorker()
	watcher.now = func() time.Time { return time.UnixMilli(beforeWatch.UpdatedAt + 1000) }
	require.NoError(t, pruneRetiredAll(ctx, watcher))
	mu.Lock()
	require.Empty(t, objects)
	late := uuid.NewString()
	objects[late] = sdk.SandboxInfo{ID: late, Metadata: spec.Metadata}
	next := Allocation{WorkID: input.WorkID, ID: uuid.NewString()}
	nextID := uuid.NewString()
	objects[nextID] = sdk.SandboxInfo{ID: nextID, Metadata: correlation(next)}
	mu.Unlock()
	require.NoError(t, pruneRetiredAll(ctx, newWorker()))
	mu.Lock()
	require.NotContains(t, objects, late)
	require.Contains(t, objects, nextID, "the same Work's newer allocation must survive old cleanup")
	require.NotContains(t, deletes, nextID)
	mu.Unlock()
	retired, _, err := latestAllocation(ctx, p.db, input.WorkID)
	require.NoError(t, err)
	require.Equal(t, beforeWatch.UpdatedAt, retired.UpdatedAt, "retired observation must not rewrite the allocation row")
	require.Equal(t, beforeWatch.Version, retired.Version)
	require.Equal(t, primary, retired.SandboxID)
	require.NotZero(t, retired.DeletedAt)
	obligations, err := pendingRemovals(ctx, removals)
	require.NoError(t, err)
	require.Len(t, obligations, 1, "surplus cleanup cannot fabricate the primary's business completion")
	// The durable watch intentionally retains retired allocations. Verify that a
	// representative history uses equality-prefix ordered access, not sorting
	// all retired rows merely to take the next bounded batch.
	rows := make([]allocationRecord, 1500)
	for i := range rows {
		rows[i] = retired
		rows[i].ID = uuid.NewString()
		rows[i].SessionID = uuid.NewString() // at most one live allocation per Work
		rows[i].UpdatedAt += int64(i)
		if i%3 == 0 {
			rows[i].DeletedAt = 0
		}
	}
	_, err = db.Insert(&rows)
	require.NoError(t, err)
	_, err = db.Exec("ANALYZE TABLE execution_allocation")
	require.NoError(t, err)
	plan, err := db.QueryString("EXPLAIN ANALYZE SELECT * FROM execution_allocation WHERE retired = 1 AND submitted = 1 AND (updated_at > ? OR (updated_at = ? AND id > ?)) ORDER BY updated_at, id LIMIT 100", retired.UpdatedAt, retired.UpdatedAt, retired.ID)
	require.NoError(t, err)
	require.Len(t, plan, 1)
	for _, text := range plan[0] {
		t.Log(text)
		require.Contains(t, text, "idx_retired_submitted_updated_at_id")
		require.NotContains(t, text, "Sort:")
		require.NotContains(t, text, "Table scan")
	}
}
