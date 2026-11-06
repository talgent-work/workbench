package connectrpc

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	agentprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/agent/private/v1"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
)

func (h *AgentHandler) GetWorkCreation(ctx context.Context, request *connect.Request[agentprivatev1.GetWorkCreationRequest]) (*connect.Response[agentprivatev1.GetWorkCreationResponse], error) {
	if err := authenticateIntegration(h.token, request.Header().Get("Authorization")); err != nil {
		return nil, err
	}
	if uuid.Validate(request.Msg.GetRequestId()) != nil {
		return nil, mapAgentError(query.ErrInvalidStartRequest)
	}
	outcome, err := h.application.Commands.CreateRequestedWork.Outcome(ctx, request.Msg.GetRequestId())
	if errors.Is(err, command.ErrWorkCreationNotFound) {
		return connect.NewResponse(&agentprivatev1.GetWorkCreationResponse{Outcome: agentprivatev1.WorkCreationOutcome_WORK_CREATION_OUTCOME_UNKNOWN}), nil
	}
	if err != nil {
		return nil, mapAgentError(err)
	}
	response := &agentprivatev1.GetWorkCreationResponse{Outcome: agentprivatev1.WorkCreationOutcome_WORK_CREATION_OUTCOME_PENDING}
	switch {
	case outcome.Canceled:
		response.Outcome = agentprivatev1.WorkCreationOutcome_WORK_CREATION_OUTCOME_CANCELLED
	case outcome.BindingConfirmed:
		response.Outcome, response.WorkId = agentprivatev1.WorkCreationOutcome_WORK_CREATION_OUTCOME_CREATED, outcome.WorkID
	}
	return connect.NewResponse(response), nil
}

func (h *AgentHandler) CancelWorkCreation(ctx context.Context, request *connect.Request[agentprivatev1.CancelWorkCreationRequest]) (*connect.Response[agentprivatev1.CancelWorkCreationResponse], error) {
	if err := authenticateIntegration(h.token, request.Header().Get("Authorization")); err != nil {
		return nil, err
	}
	if uuid.Validate(request.Msg.GetRequestId()) != nil {
		return nil, mapAgentError(query.ErrInvalidStartRequest)
	}
	workID, err := h.application.Commands.CreateRequestedWork.Cancel(ctx, request.Msg.GetRequestId())
	if err != nil {
		return nil, mapAgentError(err)
	}
	return connect.NewResponse(&agentprivatev1.CancelWorkCreationResponse{WorkId: workID}), nil
}
