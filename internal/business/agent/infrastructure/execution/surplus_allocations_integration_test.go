//go:build integration

package execution

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"

	"github.com/talgent-work/workbench/internal/business/agent/domain"

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
	newWorker := func() *executionSteps {
		p := NewProvisioning(db, provider, fixtureSandboxCredentials(db, provider), NativeLaunchConfig{Binary: "claude"}, time.Now, nil)
		connections, err := NewNativeConnections(p, p.credentials, http.DefaultClient, "http://dispatcher.invalid", "fixture")
		require.NoError(t, err)
		return newExecutionSteps(t, p, connections, unread{}, nil, time.Now)
	}
	p := NewProvisioning(db, provider, fixtureSandboxCredentials(db, provider), NativeLaunchConfig{Binary: "claude"}, time.Now, nil)
	input := request()
	record, err := register(ctx, p, input, testMounts)
	require.NoError(t, err)
	executions := NewAgentExecutionRepository(db, time.Now)
	run, err := executions.Get(ctx, record.ID)
	require.NoError(t, err)
	require.True(t, run.RecordSubmission())
	require.NoError(t, executions.Save(ctx, run))
	var spec sdk.CreateSandboxRequest
	require.NoError(t, json.Unmarshal(record.SpecificationJSON, &spec))
	first, second := uuid.NewString(), uuid.NewString()
	mu.Lock()
	objects[first] = sdk.SandboxInfo{ID: first, Metadata: spec.Metadata}
	objects[second] = sdk.SandboxInfo{ID: second, Metadata: spec.Metadata}
	holdDeletion = true
	mu.Unlock()
	// Two replicas reconcile the execution at once; the version CAS lets one
	// binding commit, and only the committed binding's surplus is deleted.
	var workers sync.WaitGroup
	for range 2 {
		workers.Go(func() { _, _ = newWorker().handler.Reconcile(ctx, record.ID) })
	}
	workers.Wait()
	record, _, err = latestAllocation(ctx, db, input.WorkID)
	require.NoError(t, err)
	require.Contains(t, []string{first, second}, record.SandboxID)
	primary := record.SandboxID
	surplus := first
	if surplus == primary {
		surplus = second
	}
	steps := newWorker()
	mu.Lock()
	require.Zero(t, creates, "duplicate discovery never authorizes a POST")
	require.NotContains(t, deletes, primary, "the durably bound primary is never deleted")
	mu.Unlock()
	require.NoError(t, steps.reconcile(ctx, record.ID))
	mu.Lock()
	require.Contains(t, objects, surplus, "DELETE acceptance can leave the environment present")
	require.NotEmpty(t, deletes)
	for _, id := range deletes {
		require.Equal(t, surplus, id, "only the surplus is deleted")
	}
	holdDeletion = false
	mu.Unlock()
	require.NoError(t, steps.reconcile(ctx, record.ID))
	mu.Lock()
	require.Contains(t, objects, primary)
	require.NotContains(t, objects, surplus)
	mu.Unlock()
	bound, err := executions.Get(ctx, record.ID)
	require.NoError(t, err)
	require.False(t, bound.Retired(), "surplus cleanup cannot fabricate the primary's business completion")

	// The process exits and the Work retires the execution: its release
	// deletes the primary.
	_, err = bound.RecordLaunch("current-process")
	require.NoError(t, err)
	bound.RecordExit("current-process", domain.HarnessExit{})
	require.NoError(t, executions.Save(ctx, bound))
	require.NoError(t, steps.retire(ctx, input.WorkID, domain.RetiredByExit, time.Now()))
	require.NoError(t, steps.reconcile(ctx, record.ID))
	mu.Lock()
	require.Empty(t, objects)
	late := uuid.NewString()
	objects[late] = sdk.SandboxInfo{ID: late, Metadata: spec.Metadata}
	next := SandboxOwner{WorkID: input.WorkID, ID: uuid.NewString()}
	nextID := uuid.NewString()
	objects[nextID] = sdk.SandboxInfo{ID: nextID, Metadata: correlation(next)}
	mu.Unlock()
	released, _, err := latestAllocation(ctx, db, input.WorkID)
	require.NoError(t, err)
	require.NotZero(t, released.ReleasedAt)
	// A late sandbox wakes the reconciliation of the released execution.
	_, err = steps.handler.Reconcile(ctx, record.ID)
	require.NoError(t, err)
	mu.Lock()
	require.NotContains(t, objects, late)
	require.Contains(t, objects, nextID, "the same Work's newer execution must survive old cleanup")
	require.NotContains(t, deletes, nextID)
	mu.Unlock()
	after, _, err := latestAllocation(ctx, db, input.WorkID)
	require.NoError(t, err)
	require.Equal(t, released.UpdatedAt, after.UpdatedAt, "a released execution's late cleanup must not rewrite its record")
	require.Equal(t, released.Version, after.Version)
	obligations, err := unreleased(ctx, db)
	require.NoError(t, err)
	require.Empty(t, obligations)
}
