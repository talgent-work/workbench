package connectrpc

import (
	"context"

	"connectrpc.com/connect"
	marketplacepublicv1 "github.com/talgent-work/workbench/gen/go/talgent/marketplace/public/v1"
	"github.com/talgent-work/workbench/internal/pkg/authn"
)

func (h *Handler) ListPersonalAuthorizations(ctx context.Context, _ *connect.Request[marketplacepublicv1.ListPersonalAuthorizationsRequest]) (*connect.Response[marketplacepublicv1.ListPersonalAuthorizationsResponse], error) {
	actor, _ := authn.CurrentUserID(ctx)
	references, err := h.application.ListPersonalAuthorizations(ctx, actor)
	if err != nil {
		return nil, mapError(err)
	}
	response := &marketplacepublicv1.ListPersonalAuthorizationsResponse{}
	for _, ref := range references {
		response.Authorizations = append(response.Authorizations, &marketplacepublicv1.PersonalAuthorization{Id: ref.ID, Provider: ref.Provider, Status: ref.Status, SupportedTargets: ref.SupportedTargets})
	}
	return connect.NewResponse(response), nil
}

func (h *Handler) CancelPersonalAuthorization(ctx context.Context, request *connect.Request[marketplacepublicv1.CancelPersonalAuthorizationRequest]) (*connect.Response[marketplacepublicv1.CancelPersonalAuthorizationResponse], error) {
	actor, _ := authn.CurrentUserID(ctx)
	reference, err := h.application.CancelPersonalAuthorization(ctx, actor, request.Msg.GetAuthorizationId())
	if err != nil {
		return nil, mapConnectionError(err)
	}
	return connect.NewResponse(&marketplacepublicv1.CancelPersonalAuthorizationResponse{Authorization: &marketplacepublicv1.PersonalAuthorization{
		Id: reference.ID, Provider: reference.Provider, Status: reference.Status,
	}}), nil
}
