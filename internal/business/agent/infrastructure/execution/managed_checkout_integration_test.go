//go:build integration

package execution

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// A start with repositories left to check out records their outcomes when
// its preparation ends, and launches its Harness only afterwards.
func TestManagedWorkLaunchesItsHarnessOnlyAfterRecordingCheckouts(t *testing.T) {
	f := newManagedWork(t)
	f.relay.set(func(m *machineRelay) {
		m.preparation = `{"results":[],"repositories":[{"repositoryId":"github:1","targetPath":"/workspace/repos/api","checkedOut":true,"reason":""},{"repositoryId":"github:2","targetPath":"/workspace/repos/private","checkedOut":false,"reason":"branch_unavailable"}]}`
	})
	input := managedRequest()
	input.RepositorySelection, input.RemainingRepositories = checkoutSelection(), []string{"github:1", "github:2"}
	begun := f.begin(t, input)
	want := []domain.RepositoryCheckout{{RepositoryID: "github:1", CheckedOut: true}, {RepositoryID: "github:2", Reason: domain.CheckoutBranchMissing}}

	var atLaunch *domain.AgentExecution
	var readErr error
	f.relay.set(func(m *machineRelay) {
		m.launching = func() { atLaunch, readErr = f.steps.executions.Get(context.Background(), begun.ID) }
	})

	f.launch(t, begun.ID)

	require.NoError(t, readErr)
	require.NotNil(t, atLaunch, "the Harness was launched")
	require.True(t, atLaunch.Process.PreparationEnded)
	require.Equal(t, want, atLaunch.CheckoutResults, "every planned outcome is recorded before the Harness process is created")
	run := current(t, f.steps, begun.ID)
	require.Equal(t, "harness-handle", run.Process.ID)
	require.Equal(t, want, run.CheckoutResults)
	require.Len(t, f.relay.created, 2)
	require.True(t, strings.HasPrefix(f.relay.created[0].Command, "talgent_checkout_plan='"), "the preparation receives the plan: %s", f.relay.created[0].Command)
	require.NotContains(t, f.relay.created[1].Command, "talgent_checkout_plan", "the Harness is not the preparation")
	ended, ok := run.CheckoutsEnded()
	require.True(t, ok)
	require.Equal(t, want, ended.Checkouts)
}
