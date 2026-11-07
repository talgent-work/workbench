package connectrpc

import (
	"context"
	"errors"

	"connectrpc.com/connect/v2"

	"github.com/talgent-work/workbench/internal/business/computer/application"
	"github.com/talgent-work/workbench/internal/business/computer/domain"
)

// publicError maps a failure to its Connect code without leaking internals.
func publicError(err error) error {
	switch {
	case errors.Is(err, domain.ErrComputerNotFound):
		return connect.NewError(connect.CodeNotFound, "computer: computer not found")
	case errors.Is(err, domain.ErrCapacityBelowOccupied):
		return connect.NewError(connect.CodeFailedPrecondition, "computer: capacity cannot be set below the Works it runs now")
	case errors.Is(err, domain.ErrDeskTaken), errors.Is(err, application.ErrDeskLeft):
		return connect.NewError(connect.CodeAlreadyExists, err.Error()).WithCause(err)
	case errors.Is(err, domain.ErrInvalidDesk), errors.Is(err, domain.ErrInvalidComputer):
		return connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	case errors.Is(err, domain.ErrCredentialRejected):
		return connect.NewError(connect.CodeUnauthenticated, "computer: computer credential rejected")
	case errors.Is(err, domain.ErrRotationUnderWay):
		return connect.NewError(connect.CodeFailedPrecondition, "computer: a rotated credential is reserved for another connection")
	case errors.Is(err, domain.ErrComputerRemoved):
		return connect.NewError(connect.CodePermissionDenied, "computer: computer removed")
	case errors.Is(err, application.ErrRegistrationNotFound):
		return connect.NewError(connect.CodeNotFound, "computer: registration not found or expired")
	case errors.Is(err, application.ErrRegistrationTaken):
		return connect.NewError(connect.CodeFailedPrecondition, "computer: another person approved this registration")
	case errors.Is(err, domain.ErrConcurrentModification):
		return connect.NewError(connect.CodeAborted, "computer: busy; try again")
	case errors.Is(err, context.Canceled):
		return connect.NewError(connect.CodeCanceled, err.Error()).WithCause(err)
	case errors.Is(err, context.DeadlineExceeded):
		return connect.NewError(connect.CodeDeadlineExceeded, err.Error()).WithCause(err)
	default:
		return connect.NewError(connect.CodeUnavailable, "computer: admission unavailable")
	}
}
