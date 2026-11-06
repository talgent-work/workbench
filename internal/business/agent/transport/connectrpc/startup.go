package connectrpc

import (
	"context"

	"connectrpc.com/connect"
	agentprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/agent/private/v1"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
)

func (h *AgentHandler) GetWorkStartup(ctx context.Context, request *connect.Request[agentprivatev1.GetWorkStartupRequest]) (*connect.Response[agentprivatev1.GetWorkStartupResponse], error) {
	if err := authenticateCaller(h.token, request.Header().Get("Authorization"), request.Msg.GetSubjectId()); err != nil {
		return nil, err
	}
	value, err := h.application.Queries.GetWorkStartup.Handle(ctx, query.GetWork{SubjectID: request.Msg.GetSubjectId(), WorkID: request.Msg.GetWorkId()})
	if err != nil {
		return nil, mapAgentError(err)
	}
	return connect.NewResponse(&agentprivatev1.GetWorkStartupResponse{WorkId: value.WorkID, InitialRequestId: value.InitialRequestID, Rejected: value.Rejected, CleanupConfirmed: value.CleanupConfirmed, CreationPending: value.CreationPending, ContinuationRequestId: value.ContinuationRequestID, ContinuationPending: value.ContinuationPending}), nil
}
