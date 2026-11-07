package connectrpc

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"connectrpc.com/connect"

	accountprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/account/private/v1"
	"github.com/talgent-work/workbench/gen/go/talgent/account/private/v1/accountprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/account/application"
	"github.com/talgent-work/workbench/internal/business/account/application/query"
	"github.com/talgent-work/workbench/internal/pkg/authn"
)

// UserDirectory serves trusted contexts only. The gateway strips
// x-talgent-service-id from every external route, so its presence marks an
// in-cluster caller.
type UserDirectory struct {
	app *application.Application
}

var _ accountprivatev1connect.UserDirectoryServiceHandler = (*UserDirectory)(nil)

func NewUserDirectory(app *application.Application) *UserDirectory {
	return &UserDirectory{app: app}
}

func (h *UserDirectory) SearchUsers(ctx context.Context, req *connect.Request[accountprivatev1.SearchUsersRequest]) (*connect.Response[accountprivatev1.SearchUsersResponse], error) {
	if err := requireService(req.Header()); err != nil {
		return nil, err
	}
	users, err := h.app.SearchUsers(ctx, req.Msg.GetUsernameQuery(), int(req.Msg.GetLimit()))
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&accountprivatev1.SearchUsersResponse{Users: directoryUsers(users)}), nil
}

func (h *UserDirectory) GetUsers(ctx context.Context, req *connect.Request[accountprivatev1.GetUsersRequest]) (*connect.Response[accountprivatev1.GetUsersResponse], error) {
	if err := requireService(req.Header()); err != nil {
		return nil, err
	}
	users, err := h.app.GetUsers(ctx, req.Msg.GetUserIds())
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&accountprivatev1.GetUsersResponse{Users: directoryUsers(users)}), nil
}

func requireService(header http.Header) error {
	if strings.TrimSpace(header.Get(authn.HeaderServiceID)) == "" {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("trusted service caller required"))
	}
	return nil
}

func mapError(err error) error {
	if errors.Is(err, application.ErrDirectoryQueryInvalid) {
		return connect.NewError(connect.CodeInvalidArgument, err)
	}
	return connect.NewError(connect.CodeInternal, err)
}

func directoryUsers(users []query.User) []*accountprivatev1.DirectoryUser {
	out := make([]*accountprivatev1.DirectoryUser, 0, len(users))
	for _, user := range users {
		out = append(out, &accountprivatev1.DirectoryUser{
			UserId:      user.ID,
			Username:    user.Username,
			Email:       user.Email,
			DisplayName: user.DisplayName,
			AvatarUrl:   user.AvatarURL,
		})
	}
	return out
}
