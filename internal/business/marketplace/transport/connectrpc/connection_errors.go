package connectrpc

import (
	"errors"

	"connectrpc.com/connect"
	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

func mapConnectionError(err error) error {
	var providerErr *application.ProviderError
	switch {
	case errors.As(err, &providerErr):
		if providerErr.Retryable {
			return connect.NewError(connect.CodeUnavailable, privateError{err})
		}
		return connect.NewError(connect.CodeFailedPrecondition, privateError{err})
	case errors.Is(err, domain.ErrPermissionDenied):
		return connect.NewError(connect.CodePermissionDenied, err)
	case errors.Is(err, domain.ErrCredentialTargetInapplicable):
		return connect.NewError(connect.CodePermissionDenied, privateError{err})
	case errors.Is(err, domain.ErrInvalidRequiredField):
		return connect.NewError(connect.CodeInvalidArgument, err)
	case errors.Is(err, domain.ErrRepositoryNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, application.ErrProviderUnavailable), errors.Is(err, domain.ErrInvalidTransition):
		return connect.NewError(connect.CodeFailedPrecondition, privateError{err})
	default:
		return mapError(err)
	}
}
