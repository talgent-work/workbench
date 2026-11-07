package connectrpc

import (
	"context"

	"connectrpc.com/connect/v2"

	accountpublicv1 "github.com/talgent-work/contracts/gen/account/public/v1"
	"github.com/talgent-work/contracts/gen/account/public/v1/accountpublicv1connect"
	"github.com/talgent-work/workbench/internal/pkg/authn"
)

// Session tells the browser who is signed in. It answers from the identity
// the gateway verified against Account's own Identity Token.
type Session struct{}

var _ accountpublicv1connect.SessionServiceHandler = Session{}

func (Session) GetCurrentSession(ctx context.Context, _ *accountpublicv1.GetCurrentSessionRequest) (*accountpublicv1.GetCurrentSessionResponse, error) {
	user, ok := authn.CurrentUser(ctx)
	if !ok || user.ID == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, "account: a signed-in person is required")
	}
	return &accountpublicv1.GetCurrentSessionResponse{Session: &accountpublicv1.CurrentSession{
		UserId: user.ID, Username: user.Username, DisplayName: user.DisplayName, PrimaryEmail: user.PrimaryEmail,
		AvatarUrl: user.AvatarURL, Roles: append([]string(nil), user.Roles...),
	}}, nil
}
