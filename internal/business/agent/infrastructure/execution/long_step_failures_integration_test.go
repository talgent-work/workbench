//go:build integration

package execution

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// A start whose retained Plugin release names another plugin in its native
// entry can never be specified: its first long step rejects it, sends no
// create, and the Work fails naming the release, as recorded.
func TestAnInvalidPluginReleaseRejectsTheStartOnItsFirstAttempt(t *testing.T) {
	ctx := context.Background()
	capabilities := domain.CapabilitySnapshot{Connectors: request().Capabilities.Connectors, Prompt: "task", Plugins: []domain.SelectedPlugin{{Release: domain.PluginRelease{
		Slug: "talgent", Source: "fixture", Version: "1.0.0", RetrievalRef: "archive:talgent", NativeEntry: []byte(`{"name":"talgent-runtime","version":"1.0.0"}`)}}}}
	f := newCloudStartWith(t, capabilities)
	require.True(t, f.reconcile(t), "the creation's submission is a long step")

	require.NoError(t, f.steps.handler.RunLongStep(ctx, f.allocation(t).ID), "nothing is left to retry")
	require.Zero(t, f.creates(), "no create is sent for a start that cannot be specified")
	run, err := f.steps.executions.Latest(ctx, f.workID)
	require.NoError(t, err)
	require.True(t, run.Outcome.Rejected)
	want := "specifying the execution environment: Plugin release talgent 1.0.0 is invalid: its native entry is named talgent-runtime"
	require.Equal(t, want, run.Outcome.RejectionReason, "the cause is recorded with the rejection")

	f.recover(t)
	work := f.work(t)
	require.Equal(t, domain.WorkFailed, work.State)
	require.True(t, work.HarnessProcess.StartRejected)
	require.Equal(t, "The execution environment could not be created for this start: "+want+".", work.FailureReason)
}

// A create whose answer is lost is never sent again; when nothing is ever
// observed, the Startup Deadline ends the start, and its failure names the
// step that failed last.
func TestAStartupDeadlineFailureNamesTheLastSetback(t *testing.T) {
	f := newUnboundStart(t, createdThenDeleted)
	run, err := f.steps.executions.Latest(context.Background(), f.workID)
	require.NoError(t, err)
	require.Equal(t, "creating the execution environment failed", run.StartupSetback, "the setback is recorded")

	f.advance(domain.StartupDeadline + 2*time.Minute)
	f.reconcile(t)
	f.recover(t)
	work := f.work(t)
	require.Equal(t, domain.WorkFailed, work.State)
	require.Equal(t, "Startup Deadline reached before the execution environment was ready. Last error: creating the execution environment failed.", work.FailureReason)
}
