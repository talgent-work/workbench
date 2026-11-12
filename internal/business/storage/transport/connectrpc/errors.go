// Package connectrpc serves Storage's private contracts to the contexts that
// locate Directories, each authenticated by its own control token, and its Artifact
// commands to Work Agents.
package connectrpc

import (
	"context"
	"crypto/subtle"
	"errors"
	"strings"

	storageprivatev1 "github.com/talgent-work/contracts/gen/go/storage/private/v1"
	"github.com/talgent-work/workbench/internal/pkg/storageerror"

	"connectrpc.com/connect/v2"

	"github.com/talgent-work/workbench/internal/business/storage/application"
	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

// authenticate admits a caller holding the control token configured for it.
func authenticate(header, token string) error {
	if strings.TrimSpace(token) == "" || subtle.ConstantTimeCompare([]byte(header), []byte("Bearer "+token)) != 1 {
		return connect.NewError(connect.CodeUnauthenticated, "storage: control authentication required")
	}
	return nil
}

// authenticateAny admits a caller holding any of the control tokens
// configured for the contract.
func authenticateAny(header string, tokens []string) error {
	for _, token := range tokens {
		if authenticate(header, token) == nil {
			return nil
		}
	}
	return connect.NewError(connect.CodeUnauthenticated, "storage: control authentication required")
}

func mapError(err error) error {
	if err == nil {
		return nil
	}
	var connectErr *connect.Error
	if errors.As(err, &connectErr) {
		return connectErr
	}
	switch {
	case errors.Is(err, context.Canceled):
		return connect.NewError(connect.CodeCanceled, err.Error()).WithCause(err)
	case errors.Is(err, context.DeadlineExceeded):
		return connect.NewError(connect.CodeDeadlineExceeded, err.Error()).WithCause(err)
	case errors.Is(err, domain.ErrSpaceTokenRejected):
		return connect.NewError(connect.CodePermissionDenied, err.Error()).WithCause(err)
	case errors.Is(err, application.ErrSourceNotFound):
		return storageerror.New(connect.CodeNotFound, storageprivatev1.ErrorReason_ERROR_REASON_SOURCE_NOT_FOUND, err)
	case errors.Is(err, application.ErrFileNotFound), errors.Is(err, domain.ErrShareLinkNotFound), errors.Is(err, domain.ErrShareLinkRevoked):
		return storageerror.New(connect.CodeNotFound, storageprivatev1.ErrorReason_ERROR_REASON_FILE_NOT_FOUND, err)
	case errors.Is(err, application.ErrNameTaken):
		return storageerror.New(connect.CodeAlreadyExists, storageprivatev1.ErrorReason_ERROR_REASON_NAME_TAKEN, err)
	case errors.Is(err, application.ErrInvalidSource):
		return storageerror.New(connect.CodeInvalidArgument, storageprivatev1.ErrorReason_ERROR_REASON_INVALID_SOURCE, err)
	case errors.Is(err, application.ErrInvalidWorkspacePath):
		return storageerror.New(connect.CodeInvalidArgument, storageprivatev1.ErrorReason_ERROR_REASON_INVALID_WORKSPACE_PATH, err)
	case errors.Is(err, domain.ErrInvalidFileName), errors.Is(err, domain.ErrInvalidPreviewKey):
		return storageerror.New(connect.CodeInvalidArgument, storageprivatev1.ErrorReason_ERROR_REASON_INVALID_NAME, err)
	case errors.Is(err, domain.ErrInvalidLocator):
		return storageerror.New(connect.CodeInvalidArgument, storageprivatev1.ErrorReason_ERROR_REASON_INVALID_LOCATOR, err)
	case errors.Is(err, domain.ErrMappingOutsidePrefix):
		return storageerror.New(connect.CodePermissionDenied, storageprivatev1.ErrorReason_ERROR_REASON_MAPPING_OUTSIDE_PREFIX, err)
	case errors.Is(err, domain.ErrMappingOverlaps):
		return storageerror.New(connect.CodeAlreadyExists, storageprivatev1.ErrorReason_ERROR_REASON_MAPPING_OVERLAPS, err)
	case errors.Is(err, domain.ErrGrantedReadWrite):
		return storageerror.New(connect.CodeFailedPrecondition, storageprivatev1.ErrorReason_ERROR_REASON_DIRECTORY_GRANTED_READ_WRITE, err)
	case errors.Is(err, domain.ErrBackendNotMappable):
		return storageerror.New(connect.CodeFailedPrecondition, storageprivatev1.ErrorReason_ERROR_REASON_BACKEND_NOT_MAPPABLE, err)
	case errors.Is(err, domain.ErrDirectoryMapped):
		return storageerror.New(connect.CodeFailedPrecondition, storageprivatev1.ErrorReason_ERROR_REASON_DIRECTORY_MAPPED, err)
	case errors.Is(err, application.ErrInvalidRequest), errors.Is(err, domain.ErrActorRequired), errors.Is(err, domain.ErrFileWriterRequired),
		errors.Is(err, domain.ErrInvalidContentPath), errors.Is(err, domain.ErrInvalidMountPoint), errors.Is(err, domain.ErrInvalidWorkspaceLayout),
		errors.Is(err, domain.ErrInvalidMappedPath):
		return storageerror.New(connect.CodeInvalidArgument, storageprivatev1.ErrorReason_ERROR_REASON_INVALID_REQUEST, err)
	case errors.Is(err, domain.ErrDirectoryNotOpen):
		return storageerror.New(connect.CodeFailedPrecondition, storageprivatev1.ErrorReason_ERROR_REASON_DIRECTORY_NOT_OPEN, err)
	case errors.Is(err, domain.ErrDirectoryNotAllocated):
		return storageerror.New(connect.CodeFailedPrecondition, storageprivatev1.ErrorReason_ERROR_REASON_DIRECTORY_NOT_ALLOCATED, err)
	case errors.Is(err, domain.ErrDirectoryReclaimed):
		return storageerror.New(connect.CodeFailedPrecondition, storageprivatev1.ErrorReason_ERROR_REASON_DIRECTORY_RECLAIMED, err)
	case errors.Is(err, domain.ErrPermanentDirectory):
		return storageerror.New(connect.CodeFailedPrecondition, storageprivatev1.ErrorReason_ERROR_REASON_PERMANENT_DIRECTORY, err)
	case errors.Is(err, domain.ErrConcurrentModification):
		return connect.NewError(connect.CodeAborted, err.Error()).WithCause(err)
	default:
		// Work Agents call these handlers too, so the message names no cause;
		// the cause stays for the completion log.
		return connect.NewError(connect.CodeInternal, "storage: internal error").WithCause(err)
	}
}
