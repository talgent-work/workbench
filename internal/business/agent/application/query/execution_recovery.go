package query

import "context"

// ExecutionRecoveryReader, a facet of ExecutionQueryRepository, finds the
// AgentExecutions with unfinished obligations and those a published change
// concerns.
type ExecutionRecoveryReader interface {
	// ListOutstandingExecutions pages executions that are not retired, or
	// retired and not released, in identity order after afterID.
	ListOutstandingExecutions(ctx context.Context, afterID string, limit int) ([]string, error)
	// ListExecutionsUsingAuthorization lists the unretired executions whose
	// Work selected the Provider Authorization.
	ListExecutionsUsingAuthorization(ctx context.Context, authorizationID string) ([]string, error)
	// ListExecutionsReadingInputs lists the unretired Managed executions
	// whose Work's input source is owner's File Space.
	ListExecutionsReadingInputs(ctx context.Context, owner string) ([]string, error)
}
