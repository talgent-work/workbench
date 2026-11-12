package connectrpc

import (
	"context"
	"errors"

	"connectrpc.com/connect/v2"

	accountprivatev1 "github.com/talgent-work/contracts/gen/go/account/private/v1"
	"github.com/talgent-work/contracts/gen/go/account/private/v1/accountprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/account/application"
	"github.com/talgent-work/workbench/internal/business/account/domain"
)

// ApplicationOwnership answers trusted contexts who owns an Application. The
// gateway strips x-talgent-service-id from every external route, so its
// presence marks an in-cluster caller.
type ApplicationOwnership struct {
	app *application.Application
}

var _ accountprivatev1connect.ApplicationOwnershipServiceHandler = (*ApplicationOwnership)(nil)

func NewApplicationOwnership(app *application.Application) *ApplicationOwnership {
	return &ApplicationOwnership{app: app}
}

func (h *ApplicationOwnership) GetApplication(ctx context.Context, req *accountprivatev1.GetApplicationRequest) (*accountprivatev1.GetApplicationResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	if err := requireService(info.RequestHeader()); err != nil {
		return nil, err
	}
	found, err := h.app.ApplicationOwnership(ctx, req.GetApplicationId())
	if errors.Is(err, domain.ErrApplicationNotFound) {
		return nil, connect.NewError(connect.CodeNotFound, "account: no such application").WithCause(err)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return &accountprivatev1.GetApplicationResponse{Application: &accountprivatev1.OwnedApplication{
		ApplicationId: found.ID, Name: found.Name, OwnerUserId: found.Owner, Removed: found.Removed,
	}}, nil
}
