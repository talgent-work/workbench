//go:build integration

package agent

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"xorm.io/xorm"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"
)

// A committed execution change wakes the execution's reconciliation and
// dispatches the events it recorded, once; a change that loses to a
// concurrent one does neither.
func TestCommittedExecutionWakesAndDispatchesOnlyAfterItsCommit(t *testing.T) {
	ctx := context.Background()
	db, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	events := &recordedEvents{}
	var woken []string
	executions := newCommittedExecutions(db, time.Now, &workStateUpdates{reader: unavailableViews{}, logger: slog.New(slog.DiscardHandler)}, events)
	executions.wake = func(_ context.Context, id string) { woken = append(woken, id) }

	launch := domain.LaunchSpecification{WorkModel: domain.WorkModelSelection{ConfigurationID: "default"}, WorkID: "committed-work", HarnessSessionReference: "context",
		ContextMode: domain.ContextContinue, WorkspaceReference: "workspace", HomeBinding: domain.HomeBinding{OwnerID: "owner", Namespace: "project"}}
	run, err := domain.BeginExecution(domain.ExecutionBeginning{Admitted: domain.WorkStartAdmitted{WorkID: launch.WorkID, StartAdmittedAt: time.UnixMilli(time.Now().UnixMilli()), DeskID: "desk"}, Launch: launch, Realization: domain.RealizationCloud})
	require.NoError(t, err)
	require.NoError(t, executions.Save(ctx, run))
	require.Equal(t, []string{run.ID}, woken, "a committed execution's reconciliation is woken")
	events.dispatched = nil

	current, err := executions.Get(ctx, run.ID)
	require.NoError(t, err)
	stale, err := executions.Get(ctx, run.ID)
	require.NoError(t, err)
	current.RecordSubmission()
	_, err = current.RecordCreationAnswer(domain.CreationAnswer{EnvironmentID: current.ID})
	require.NoError(t, err)
	_, err = current.RecordLaunch("process")
	require.NoError(t, err)
	changed, err := current.ConfirmReady(domain.ReadinessReport{Prepared: true, Started: true}, time.Now())
	require.NoError(t, err)
	require.True(t, changed)
	require.NoError(t, executions.Save(ctx, current))
	require.Len(t, events.dispatched, 1)
	require.Equal(t, domain.EventKindExecutionReady, events.dispatched[0].Kind())
	require.Equal(t, []string{run.ID, run.ID}, woken)

	require.Zero(t, current.Events.Len(), "dispatched events are drained")

	stale.RecordSubmission()
	_, err = stale.RecordCreationAnswer(domain.CreationAnswer{EnvironmentID: "other"})
	require.NoError(t, err)
	require.ErrorIs(t, executions.Save(ctx, stale), domain.ErrConcurrentModification)
	require.Len(t, events.dispatched, 1, "a lost change dispatches nothing")
	require.Len(t, woken, 2, "a lost change wakes nothing")
}
