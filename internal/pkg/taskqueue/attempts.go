package taskqueue

import (
	"context"

	componenttaskqueue "github.com/go-jimu/components/taskqueue"
)

// LastAttempt reports whether the task attempt running under ctx is its last:
// it already retried as often as its ceiling allows, so a failure now ends
// it. Without retry metadata it reports false.
func LastAttempt(ctx context.Context) bool {
	execution, ok := componenttaskqueue.ExecutionInfoFromContext(ctx)
	if !ok {
		return false
	}
	retried, counted := execution.RetryCount()
	ceiling, capped := execution.MaxRetry()
	return counted && capped && retried >= ceiling
}
