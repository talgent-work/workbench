package connectrpc

import (
	"context"

	agentprivatev1 "github.com/talgent-work/contracts/gen/go/agent/private/v1"
	"github.com/talgent-work/contracts/gen/go/agent/private/v1/agentprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/agent/application"
)

// WorkAttributionHandler serves trusted contexts the Work Labels of an
// Application's Works, which they keep no copy of.
type WorkAttributionHandler struct {
	application *application.Application
	token       string
}

var _ agentprivatev1connect.WorkAttributionServiceHandler = (*WorkAttributionHandler)(nil)

func NewWorkAttributionHandler(app *application.Application, token string) *WorkAttributionHandler {
	return &WorkAttributionHandler{application: app, token: token}
}

func (h *WorkAttributionHandler) ListWorkAttributions(ctx context.Context, request *agentprivatev1.ListWorkAttributionsRequest) (*agentprivatev1.ListWorkAttributionsResponse, error) {
	if err := authenticateIntegration(h.token, requestHeader(ctx).Get("Authorization")); err != nil {
		return nil, err
	}
	works, err := h.application.ListWorkAttributions(ctx, request.GetApplicationId(), request.GetWorkIds())
	if err != nil {
		return nil, mapAgentError(err)
	}
	response := &agentprivatev1.ListWorkAttributionsResponse{Works: make([]*agentprivatev1.WorkAttribution, 0, len(works))}
	for _, work := range works {
		response.Works = append(response.Works, &agentprivatev1.WorkAttribution{WorkId: work.WorkID, Labels: work.Labels})
	}
	return response, nil
}
