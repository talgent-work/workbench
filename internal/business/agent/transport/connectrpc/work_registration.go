package connectrpc

import (
	"context"

	"connectrpc.com/connect"
	agentprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/agent/private/v1"
	"github.com/talgent-work/workbench/internal/business/agent/application"
)

func (h *AgentHandler) CancelWorkRegistration(ctx context.Context, req *connect.Request[agentprivatev1.CancelWorkRegistrationRequest]) (*connect.Response[agentprivatev1.CancelWorkRegistrationResponse], error) {
	if err := authenticateCaller(h.token, req.Header().Get("Authorization"), req.Msg.GetSubjectId()); err != nil {
		return nil, err
	}
	id, err := h.application.CancelWorkRegistration(ctx, application.CancelWorkRegistration{
		RequestID: req.Msg.GetRequestId(), SubjectID: req.Msg.GetSubjectId(), AgentID: req.Msg.GetAgentId(),
		Subject: subjectFromProto(req.Msg.GetSubject()), ProjectID: req.Msg.GetProjectId(), WorkspaceReference: req.Msg.GetWorkspaceReference(),
	})
	if err != nil {
		return nil, mapAgentError(err)
	}
	return connect.NewResponse(&agentprivatev1.CancelWorkRegistrationResponse{WorkId: id}), nil
}
