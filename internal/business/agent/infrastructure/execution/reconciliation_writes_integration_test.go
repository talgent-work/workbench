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
	p := NewProvisioning(db, provider, credentials, NativeLaunchConfig{Binary: "claude"}, time.Now, nil)
	input := request()
	live, err := register(ctx, p, input, testMounts)
	require.NoError(t, err)
	history := make([]executionRecord, 105)
	for i := range history {
		history[i] = live
		history[i].ID = fmt.Sprintf("01900000-0000-7000-8000-%012d", i)
		history[i].SessionID = fmt.Sprintf("closed-work-%03d", i)
		closed := input
		closed.WorkID = history[i].SessionID
		history[i].RequestJSON, err = encodeLaunch(closed)
		require.NoError(t, err)
		history[i].OwnershipLabel = storageKey(history[i].ID)[:48] // MySQL must derive the same label
		history[i].Submitted = true
		history[i].State, history[i].Retirement = "released", "exited"
		history[i].DeletedAt = 1
		history[i].ReleasedAt = 2
	}
	_, err = db.Insert(&history)
	require.NoError(t, err)
	history = nil // as stored, with its JSON columns normalized
	require.NoError(t, db.Where("retired = 1").Asc("id").Find(&history))
	require.Len(t, history, 105)

	var mu sync.Mutex
	lists := map[string]int{}
	providerAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		filter, _ := url.ParseQuery(r.URL.Query().Get("metadata"))
		lists[filter.Get(sandboxOwnershipLabel)]++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	defer providerAPI.Close()
	provider.client, err = shared.NewLifecycleClient(providerAPI.Client(), shared.Config{Address: providerAPI.URL + "/v1", APIKey: "fixture"})
	require.NoError(t, err)
	connections, err := NewNativeConnections(p, credentials, http.DefaultClient, "http://dispatcher.invalid", "fixture")
	require.NoError(t, err)
	steps := newExecutionSteps(t, p, connections, unread{}, nil, time.Now)

	outstanding, err := infrastructure.NewExecutionQueryRepository(db).ListOutstandingExecutions(ctx, "", 1000)
	require.NoError(t, err)
	require.Equal(t, []string{live.ID}, outstanding, "only the live execution has unfinished obligations")

	late := history[42]
	found, ok, err := p.forSandbox(ctx, late.OwnershipLabel)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, late.ID, found)
	// A late sandbox wakes the reconciliation of the released execution.
	_, err = steps.handler.Reconcile(ctx, late.ID)
	require.NoError(t, err)
	mu.Lock()
	require.Equal(t, map[string]int{late.OwnershipLabel: 1}, lists, "only the observed allocation is checked")
	mu.Unlock()
	var actual []executionRecord
	require.NoError(t, db.Where("retired = 1").Asc("id").Find(&actual))
	require.Equal(t, history, actual, "observation must not emit full-row binlog updates")
}

func TestUnknownSandboxLabelProvesNoOwnership(t *testing.T) {
	ctx := context.Background()
	db, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	_, found, err := records{db: db}.forSandbox(ctx, storageKey("not-an-allocation")[:48])
	require.NoError(t, err)
	require.False(t, found)
}
