package execution

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// A preparation process ended by the outer timeout before its report keeps the
// checkouts that already settled; only the unfinished one timed out.
func TestCheckoutsSettledBeforeATimeoutKillAreKept(t *testing.T) {
	plan := []repositoryCheckout{{RepositoryID: "github:1", TargetPath: "/workspace/repos/a"}, {RepositoryID: "github:2", TargetPath: "/workspace/repos/b"}}
	output := []byte(`{"checkout":{"repositoryId":"github:1","targetPath":"/workspace/repos/a","checkedOut":true,"reason":""}}
{"checkout":{"repositoryId":"github:9","targetPath":"/workspace/repos/x","checkedOut":true,"reason":""}}
{"checkout":{"repositoryId":"github:2","targetPath":"/elsewhere","checkedOut":true,"reason":""}}
`)
	report := latestReport(output, timeoutExitCode, domain.CapabilitySnapshot{}, plan)
	require.Nil(t, report.Repositories)
	report.Repositories = settledCheckouts(output, plan)
	require.Equal(t, []command.RepositoryCheckoutResult{
		{RepositoryID: "github:1", TargetPath: "/workspace/repos/a", Prepared: true},
	}, report.checkouts(), "unplanned or misplaced lines are ignored; github:2 is left to the plan")
	require.Equal(t, command.CheckoutTimedOut, unfinishedCheckout(timeoutExitCode))
}
