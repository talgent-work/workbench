package query

import (
	"context"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// ExecutionQueryRepository reads what the AgentExecutions recorded without
// loading them: the current run's Plugin and service access outcomes, the
// recovery candidates, a Computer's running, held and owed-Resume Works, and
// the credential and telemetry token issued to a Work's current live run.
type ExecutionQueryRepository interface {
	// ListWorkIDs pages, in identity order after afterID, the Works with
	// outstanding obligations, completed ones whose compute release was
	// interrupted included.
	ListWorkIDs(ctx context.Context, afterID string, limit int) ([]string, error)
	// ListOutstandingExecutions pages executions that are not retired, or
	// retired and not released, in identity order after afterID.
	ListOutstandingExecutions(ctx context.Context, afterID string, limit int) ([]string, error)
	// ListExecutionsUsingAuthorization lists the unretired executions whose
	// Work selected the Provider Authorization.
	ListExecutionsUsingAuthorization(ctx context.Context, authorizationID string) ([]string, error)
	// ListExecutionsReadingInputs lists the unretired Managed executions
	// whose Work's located inputs are the Directory.
	ListExecutionsReadingInputs(ctx context.Context, directory domain.Directory) ([]string, error)
	// ListRunningWorkIDs lists, in identity order after afterID, a
	// Computer's Works whose run may still be in an execution environment on
	// it: Starting past its Desk, Working, Stopping, or Closing before its
	// compute release.
	ListRunningWorkIDs(ctx context.Context, computerID, afterID string, limit int) ([]string, error)
	// ListHeldWorkIDs lists, in identity order after afterID, a Computer's
	// Works whose run's compute is not released, a start waiting in its line
	// and a Failed Work included.
	ListHeldWorkIDs(ctx context.Context, computerID, afterID string, limit int) ([]string, error)
	// ListReachResumeWorkIDs lists, in identity order after afterID, a
	// Computer's Stopped Works marked ReachResumeDue.
	ListReachResumeWorkIDs(ctx context.Context, computerID, afterID string, limit int) ([]string, error)
	// ListCurrentPluginResults reads the Plugin outcomes of a Work's current
	// run: those its most recently begun AgentExecution recorded (Plugin
	// result reading).
	ListCurrentPluginResults(ctx context.Context, workID string) ([]PluginResult, error)
	ReadServiceAccess(ctx context.Context, workID string) ([]ServiceAccessObservation, error)
	ReadRepositoryPreparation(ctx context.Context, workID string) ([]RepositoryPreparation, error)
	// RunTelemetryAttribution authenticates the telemetry a Managed Work's
	// run sends by the telemetry token issued to the Work's current live
	// execution, and answers the resource attribution everything that run
	// sends carries, which replaces whatever the machine attributed itself.
	// The token writes only that run's telemetry: it is revoked with the
	// execution and grants no other authority. Any other token is rejected
	// with ErrTelemetryTokenRejected.
	RunTelemetryAttribution(ctx context.Context, workID, token string) (map[string]string, error)
}
