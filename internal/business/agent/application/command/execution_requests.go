package command

import (
	"context"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// ExecutionRequests captures Domain-selected requests for delivery after persistence.
type ExecutionRequests interface {
	domain.ExecutionPort
	Send(context.Context) error
}

// ExecutionRequestsFactory creates an isolated delivery scope for each command.
type ExecutionRequestsFactory func() ExecutionRequests
