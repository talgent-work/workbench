package connectrpc

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectinprocess"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

// A refusal tells the consumer whether it confirms anything about the
// authorization: a deleted one is as unusable as a revoked one, while an
// unexplained failure stays "unavailable" for the consumer to retry.
func TestCredentialRefusalsNameConfirmedReasons(t *testing.T) {
	spec := connect.Spec{StreamType: connect.StreamTypeUnary, Procedure: "/test.Service/Supply"}
	for err, reason := range map[error]string{
		domain.ErrAuthorizationRevoked:           "revoked",
		domain.ErrAuthorizationReconnectRequired: "reconnect_required",
		domain.ErrProviderNotFound:               "revoked",
		domain.ErrPermissionDenied:               "access_denied",
		errors.New("github timed out"):           "unavailable",
	} {
		server := connect.NewServer()
		server.Register(connect.Method{Spec: spec, Handler: func(ctx context.Context, _ connect.Spec, stream connect.ServerStream) error {
			if receiveErr := stream.Receive(&emptypb.Empty{}); receiveErr != nil {
				return receiveErr
			}
			return credentialSupplyError(ctx, err)
		}})
		ctx, info := connect.NewClientContext(context.Background())
		callErr := connect.NewClient(connectinprocess.New(server)).CallUnary(ctx, spec, &emptypb.Empty{}, &emptypb.Empty{})
		var refused *connect.Error
		require.ErrorAs(t, callErr, &refused)
		require.Equal(t, connect.CodeFailedPrecondition, refused.Code(), err.Error())
		require.Equal(t, reason, info.ResponseHeader().Get(CredentialStatusHeader), err.Error())
	}
}
