//go:build integration

package execution

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"xorm.io/xorm"
)

func TestRemovalSurvivesReconnectUntilSandboxAbsence(t *testing.T) {
	ctx := context.Background()
	dsn := mysqltest.Database(t)
	db, err := xorm.NewEngine("mysql", dsn)
	require.NoError(t, err)
	sandboxes, _, _ := sandboxClient(t)
	input := request()
	resource, err := sandboxes.submitRequest(ctx, input, "execd-secret")
	require.NoError(t, err)
	removals := NewRemovals(db, time.Now)
	require.NoError(t, removals.Request(ctx, owner(input), resource))
	require.NoError(t, removals.Request(ctx, owner(input), resource))
	require.ErrorIs(t, removals.Request(ctx, owner(input), "4d21dd91-f41a-443b-a77a-d303a160c29d"), errEnvironmentConflict)
	other := owner(input)
	other.WorkID = "other-session"
	require.ErrorIs(t, removals.Request(ctx, other, resource), errEnvironmentConflict)
	require.NoError(t, db.Close())

	db, err = xorm.NewEngine("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	removals = NewRemovals(db, time.Now)

	feedback := &reclamationFeedback{err: connect.NewError(connect.CodeUnavailable, errors.New("Agent offline"))}
	reclaimer, err := NewReclaimer(feedback)
	require.NoError(t, err)
	cloud := cloudEnvironment{sandboxes: sandboxes, credentials: fixtureSandboxCredentials(db, sandboxes)}
	require.NoError(t, reclaimPending(ctx, reclaimer, removals, cloud)) // DELETE accepted.
	records, err := pendingRemovals(ctx, removals)
	require.NoError(t, err)
	require.Len(t, records, 1)
	require.Empty(t, feedback.requests)
	stale := records[0]
	// The observed absence completes the obligation before Agent learns of
	// it; Agent being unavailable leaves the confirmation to the Work's own
	// recovery, whose observation finds the sandbox absent.
	require.Error(t, reclaimPending(ctx, reclaimer, removals, cloud)) // Absent, Agent unavailable.
	records, err = pendingRemovals(ctx, removals)
	require.NoError(t, err)
	require.Empty(t, records, "a released sandbox is not released again")
	require.Len(t, feedback.requests, 1, "no hidden feedback retry")
	require.Equal(t, input.WorkID, feedback.requests[0].WorkID)
	require.Equal(t, command.ComputeReclaimed, feedback.requests[0].Kind, "observed absence confirms compute release")
	require.ErrorIs(t, removals.complete(ctx, stale), errEnvironmentConflict, "a stale observation cannot complete the removal again")
	require.NoError(t, removals.Request(ctx, owner(input), resource))
	records, err = pendingRemovals(ctx, removals)
	require.NoError(t, err)
	require.Empty(t, records)
	feedback.err = nil
	require.NoError(t, reclaimPending(ctx, reclaimer, removals, cloud))
	require.Len(t, feedback.requests, 1)
}
