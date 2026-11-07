package connectrpc

import (
	"context"

	"github.com/google/uuid"
	agentprivatev1 "github.com/talgent-work/contracts/gen/go/agent/private/v1"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
)

func (h *AgentHandler) GetWorkCreation(ctx context.Context, request *agentprivatev1.GetWorkCreationRequest) (*agentprivatev1.GetWorkCreationResponse, error) {
	if err := authenticateIntegration(h.token, requestHeader(ctx).Get("Authorization")); err != nil {
		return nil, err
	}
	if uuid.Validate(request.GetRequestId()) != nil {
		return nil, mapAgentError(query.ErrInvalidStartRequest)
	}
	outcome, found, err := h.application.Commands.CreateWork.Outcome(ctx, request.GetRequestId())
	if err != nil {
		return nil, mapAgentError(err)
	}
	// A creation is recorded only once it committed or was settled, so an
	// identity without a record is one Agent has not received or answered.
	response := &agentprivatev1.GetWorkCreationResponse{Outcome: agentprivatev1.WorkCreationOutcome_WORK_CREATION_OUTCOME_UNKNOWN}
	switch {
	case !found:
	case outcome.Canceled:
		response.Outcome = agentprivatev1.WorkCreationOutcome_WORK_CREATION_OUTCOME_CANCELLED
	default:
		response.Outcome, response.WorkId = agentprivatev1.WorkCreationOutcome_WORK_CREATION_OUTCOME_CREATED, outcome.WorkID
	}
	return response, nil
}

func (h *AgentHandler) CancelWorkCreation(ctx context.Context, request *agentprivatev1.CancelWorkCreationRequest) (*agentprivatev1.CancelWorkCreationResponse, error) {
	if err := authenticateIntegration(h.token, requestHeader(ctx).Get("Authorization")); err != nil {
		return nil, err
	}
	if uuid.Validate(request.GetRequestId()) != nil {
		return nil, mapAgentError(query.ErrInvalidStartRequest)
	}
	workID, err := h.application.Commands.CreateWork.Cancel(ctx, request.GetRequestId())
	if err != nil {
		return nil, mapAgentError(err)
	}
	return &agentprivatev1.CancelWorkCreationResponse{WorkId: workID}, nil
}
