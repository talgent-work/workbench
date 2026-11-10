package connectrpc

import (
	"context"
	"strings"

	agentprivatev1 "github.com/talgent-work/contracts/gen/go/agent/private/v1"
	"github.com/talgent-work/contracts/gen/go/agent/private/v1/agentprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
)

// ActingWorkHandler answers trusted contexts whether a Work may act now,
// before they admit a write of its Agent (Acting check).
type ActingWorkHandler struct {
	application *application.Application
	token       string
}

var _ agentprivatev1connect.ActingWorkServiceHandler = (*ActingWorkHandler)(nil)

func NewActingWorkHandler(app *application.Application, token string) *ActingWorkHandler {
	return &ActingWorkHandler{application: app, token: token}
}

func (h *ActingWorkHandler) AdmitAgentAction(ctx context.Context, request *agentprivatev1.AdmitAgentActionRequest) (*agentprivatev1.AdmitAgentActionResponse, error) {
	if err := authenticateIntegration(h.token, requestHeader(ctx).Get("Authorization")); err != nil {
		return nil, err
	}
	if err := h.application.Queries.AdmitAgentAction.Handle(ctx, query.AdmitAgentAction{WorkID: strings.TrimSpace(request.GetWorkId())}); err != nil {
		return nil, mapAgentError(err)
	}
	return &agentprivatev1.AdmitAgentActionResponse{}, nil
}
