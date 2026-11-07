// Package connectrpc serves Storage's private contracts to the contexts that
// name spaces, each authenticated by its own control token, and its Artifact
// commands to Work Agents.
package connectrpc

import (
	"context"
	"crypto/subtle"
	"errors"
	"strings"

	storageprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/storage/private/v1"
	"github.com/talgent-work/workbench/internal/pkg/storageerror"

	"connectrpc.com/connect"

	"github.com/talgent-work/workbench/internal/business/storage/application"
	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

// authenticate admits a caller holding the control token configured for it.
func authenticate(header, token string) error {
	if strings.TrimSpace(token) == "" || subtle.ConstantTimeCompare([]byte(header), []byte("Bearer "+token)) != 1 {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("storage: control authentication required"))
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
	return connect.NewError(connect.CodeUnauthenticated, errors.New("storage: control authentication required"))
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
		return connect.NewError(connect.CodeCanceled, err)
	case errors.Is(err, context.DeadlineExceeded):
		return connect.NewError(connect.CodeDeadlineExceeded, err)
	case errors.Is(err, domain.ErrSpaceTokenRejected):
		return connect.NewError(connect.CodePermissionDenied, err)
	case errors.Is(err, application.ErrSourceNotFound):
		return storageerror.New(connect.CodeNotFound, storageprivatev1.ErrorReason_ERROR_REASON_SOURCE_NOT_FOUND, err)
	case errors.Is(err, application.ErrFileNotFound), errors.Is(err, domain.ErrShareLinkNotFound), errors.Is(err, domain.ErrShareLinkRevoked):
		return storageerror.New(connect.CodeNotFound, storageprivatev1.ErrorReason_ERROR_REASON_FILE_NOT_FOUND, err)
	case errors.Is(err, application.ErrNameTaken):
		return storageerror.New(connect.CodeAlreadyExists, storageprivatev1.ErrorReason_ERROR_REASON_NAME_TAKEN, err)
	case errors.Is(err, application.ErrInvalidSource), errors.Is(err, domain.ErrSentContentWithWorkspace):
		return storageerror.New(connect.CodeInvalidArgument, storageprivatev1.ErrorReason_ERROR_REASON_INVALID_SOURCE, err)
	case errors.Is(err, application.ErrInvalidWorkspacePath):
		return storageerror.New(connect.CodeInvalidArgument, storageprivatev1.ErrorReason_ERROR_REASON_INVALID_WORKSPACE_PATH, err)
	case errors.Is(err, domain.ErrInvalidFileName), errors.Is(err, domain.ErrInvalidFileKind), errors.Is(err, domain.ErrInvalidPreviewKey):
		return storageerror.New(connect.CodeInvalidArgument, storageprivatev1.ErrorReason_ERROR_REASON_INVALID_NAME, err)
	case errors.Is(err, application.ErrInvalidRequest), errors.Is(err, domain.ErrOwnerRequired), errors.Is(err, domain.ErrInvalidOwner),
		errors.Is(err, domain.ErrSharerRequired), errors.Is(err, domain.ErrFileWriterRequired), errors.Is(err, domain.ErrInvalidHomeOwner),
		errors.Is(err, domain.ErrInvalidReferencePath), errors.Is(err, domain.ErrInvalidReferenceMount):
		return storageerror.New(connect.CodeInvalidArgument, storageprivatev1.ErrorReason_ERROR_REASON_INVALID_REQUEST, err)
	case errors.Is(err, application.ErrWorkspaceNotCreated):
		return storageerror.New(connect.CodeNotFound, storageprivatev1.ErrorReason_ERROR_REASON_WORKSPACE_NOT_CREATED, err)
	case errors.Is(err, application.ErrWorkspaceReclaimed):
		return storageerror.New(connect.CodeNotFound, storageprivatev1.ErrorReason_ERROR_REASON_WORKSPACE_RECLAIMED, err)
	case errors.Is(err, domain.ErrSpaceNotOpen):
		return storageerror.New(connect.CodeFailedPrecondition, storageprivatev1.ErrorReason_ERROR_REASON_SPACE_NOT_OPEN, err)
	case errors.Is(err, domain.ErrConcurrentModification):
		return connect.NewError(connect.CodeAborted, err)
	default:
		// Private callers only: the cause stays for the completion log.
		return connect.NewError(connect.CodeInternal, err)
	}
}
