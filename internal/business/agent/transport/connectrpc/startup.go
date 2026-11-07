package connectrpc

import (
	"context"

	agentprivatev1 "github.com/talgent-work/contracts/gen/go/agent/private/v1"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
)

func (h *AgentHandler) GetWorkStartup(ctx context.Context, request *agentprivatev1.GetWorkStartupRequest) (*agentprivatev1.GetWorkStartupResponse, error) {
	if err := authenticateCaller(h.token, requestHeader(ctx).Get("Authorization"), request.GetSubjectId()); err != nil {
		return nil, err
	}
	value, err := h.application.Queries.GetWorkStartup.Handle(ctx, query.GetWork{SubjectID: request.GetSubjectId(), WorkID: request.GetWorkId()})
	if err != nil {
		return nil, mapAgentError(err)
	}
	return &agentprivatev1.GetWorkStartupResponse{WorkId: value.WorkID, InitialRequestId: value.InitialRequestID, Rejected: value.Rejected, CleanupConfirmed: value.CleanupConfirmed, CreationPending: value.CreationPending, ContinuationRequestId: value.ContinuationRequestID, ContinuationPending: value.ContinuationPending}, nil
}
