//go:build integration

package execution

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	shared "github.com/talgent-work/workbench/internal/pkg/opensandbox"
	"xorm.io/xorm"
)

// Completed history is never scanned: only a live allocation is outstanding,
// and reconciling a retired allocation when one of its late sandboxes is
// observed checks the provider once and writes nothing.
func TestRetiredHistoryIsNotOutstandingAndObservingItWritesNothing(t *testing.T) {
	ctx := context.Background()
	db, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	provider, _, _ := sandboxClient(t)
	credentials := fixtureSandboxCredentials(db, provider)
	p := NewProvisioning(db, provider, credentials, NativeLaunchConfig{Binary: "claude"}, time.Now, infrastructure.NewInitialExecutionRequests(db, time.Now), nil)
	input := request()
	require.NoError(t, p.Register(ctx, input, testMounts))
	live, _, err := latestAllocation(ctx, p.db, input.WorkID)
	require.NoError(t, err)
	history := make([]allocationRecord, 105)
	for i := range history {
		history[i] = live
		history[i].ID = fmt.Sprintf("retired-%03d", i)
		history[i].SessionID = fmt.Sprintf("closed-work-%03d", i)
		history[i].AllocationLabel = storageKey(history[i].ID)[:48] // MySQL must derive the same label
		history[i].Submitted = true
		history[i].DeletedAt = 1
		history[i].ReleasedAt = 2
	}
	_, err = db.Insert(&history)
	require.NoError(t, err)

	var mu sync.Mutex
	lists := map[string]int{}
	providerAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		filter, _ := url.ParseQuery(r.URL.Query().Get("metadata"))
		lists[filter.Get(sandboxAllocationLabel)]++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	defer providerAPI.Close()
	provider.client, err = shared.NewLifecycleClient(providerAPI.Client(), shared.Config{Address: providerAPI.URL + "/v1", APIKey: "fixture"})
	require.NoError(t, err)
	feedback := &reclamationFeedback{}
	connections, err := NewNativeConnections(p, credentials, http.DefaultClient, "http://dispatcher.invalid", "fixture", feedback, command.NewRecordRepositoryCheckoutsHandler(infrastructure.NewInitialExecutionRequests(db, time.Now)))
	require.NoError(t, err)
	reclaimer, err := NewReclaimer(feedback)
	require.NoError(t, err)
	recovery, err := NewEnvironmentRecovery(p, connections, reclaimer, unread{})
	require.NoError(t, err)

	outstanding, err := recovery.ListOutstandingAllocations(ctx, "", 1000)
	require.NoError(t, err)
	require.Len(t, outstanding, 1, "only the live allocation has unfinished obligations")
	require.Equal(t, live.ID, outstanding[0].AllocationID)

	late := history[42]
	found, ok, err := recovery.AllocationForSandbox(ctx, late.AllocationLabel)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, late.ID, found)
	_, err = recovery.ReconcileAllocation(ctx, late.ID)
	require.NoError(t, err)
	mu.Lock()
	require.Equal(t, map[string]int{late.AllocationLabel: 1}, lists, "only the observed allocation is checked")
	mu.Unlock()
	var actual []allocationRecord
	require.NoError(t, db.Where("retired = 1").Asc("id").Find(&actual))
	require.Equal(t, history, actual, "observation must not emit full-row binlog updates")
}

func TestUnknownSandboxLabelProvesNoOwnership(t *testing.T) {
	ctx := context.Background()
	db, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	provider, _, _ := sandboxClient(t)
	credentials := fixtureSandboxCredentials(db, provider)
	p := NewProvisioning(db, provider, credentials, NativeLaunchConfig{Binary: "claude"}, time.Now, infrastructure.NewInitialExecutionRequests(db, time.Now), nil)
	connections, err := NewNativeConnections(p, credentials, http.DefaultClient, "http://dispatcher.invalid", "fixture", &reclamationFeedback{}, command.NewRecordRepositoryCheckoutsHandler(infrastructure.NewInitialExecutionRequests(db, time.Now)))
	require.NoError(t, err)
	reclaimer, err := NewReclaimer(&reclamationFeedback{})
	require.NoError(t, err)
	recovery, err := NewEnvironmentRecovery(p, connections, reclaimer, unread{})
	require.NoError(t, err)
	_, found, err := recovery.AllocationForSandbox(ctx, storageKey("not-an-allocation")[:48])
	require.NoError(t, err)
	require.False(t, found)
}
