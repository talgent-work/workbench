package connectrpc

import (
	"context"

	"connectrpc.com/connect/v2"

	agentpublicv1 "github.com/talgent-work/contracts/gen/go/agent/public/v1"
	"github.com/talgent-work/contracts/gen/go/agent/public/v1/agentpublicv1connect"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/pkg/authn"
)

// IdentityHandler tells a Work's Agent who it is: the Work its Identity Token
// names, with the facts that Work fixed at creation as Agent keeps them.
type IdentityHandler struct{ application *application.Application }

func NewIdentityHandler(app *application.Application) IdentityHandler {
	return IdentityHandler{application: app}
}

var _ agentpublicv1connect.IdentityServiceHandler = IdentityHandler{}

func (h IdentityHandler) WhoAmI(ctx context.Context, _ *agentpublicv1.WhoAmIRequest) (*agentpublicv1.WhoAmIResponse, error) {
	agent, ok := authn.CurrentAgent(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodePermissionDenied, "agent: only a Work's Agent asks who it is")
	}
	origin, err := h.application.Queries.GetWorkOrigin.Handle(ctx, agent.WorkID)
	if err != nil {
		return nil, mapAgentError(err)
	}
	return &agentpublicv1.WhoAmIResponse{
		WorkId: origin.WorkID, WorkCaller: workCallerToProto(origin.WorkCaller), WorkKey: workKeyToProto(origin.WorkKey), FileHome: fileHomeToProto(origin.FileHome),
		Purpose: origin.Purpose, AgentId: origin.AgentID,
	}, nil
}
