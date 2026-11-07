package connectrpc

import (
	"errors"

	"connectrpc.com/connect/v2"
	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

func mapConnectionError(err error) error {
	var providerErr *application.ProviderError
	switch {
	case errors.As(err, &providerErr):
		if providerErr.Retryable {
			return connect.NewError(connect.CodeUnavailable, privateError{err}.Error())
		}
		return connect.NewError(connect.CodeFailedPrecondition, privateError{err}.Error())
	case errors.Is(err, domain.ErrPermissionDenied):
		return connect.NewError(connect.CodePermissionDenied, err.Error()).WithCause(err)
	case errors.Is(err, domain.ErrCredentialTargetInapplicable):
		return connect.NewError(connect.CodePermissionDenied, privateError{err}.Error())
	case errors.Is(err, domain.ErrInvalidRequiredField):
		return connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	case errors.Is(err, domain.ErrRepositoryNotFound):
		return connect.NewError(connect.CodeNotFound, err.Error()).WithCause(err)
	case errors.Is(err, application.ErrProviderUnavailable), errors.Is(err, domain.ErrInvalidTransition):
		return connect.NewError(connect.CodeFailedPrecondition, privateError{err}.Error())
	default:
		return mapError(err)
	}
}
