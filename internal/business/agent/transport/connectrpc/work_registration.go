package connectrpc

import (
	"context"

	agentprivatev1 "github.com/talgent-work/contracts/gen/go/agent/private/v1"
	"github.com/talgent-work/workbench/internal/business/agent/application"
)

func (h *AgentHandler) CancelWorkRegistration(ctx context.Context, req *agentprivatev1.CancelWorkRegistrationRequest) (*agentprivatev1.CancelWorkRegistrationResponse, error) {
	if err := authenticateCaller(h.token, requestHeader(ctx).Get("Authorization"), req.GetSubjectId()); err != nil {
		return nil, err
	}
	id, err := h.application.CancelWorkRegistration(ctx, application.CancelWorkRegistration{
		RequestID: req.GetRequestId(), SubjectID: req.GetSubjectId(), AgentID: req.GetAgentId(),
		Subject: subjectFromProto(req.GetSubject()), ProjectID: req.GetProjectId(), WorkspaceReference: req.GetWorkspaceReference(),
	})
	if err != nil {
		return nil, mapAgentError(err)
	}
	return &agentprivatev1.CancelWorkRegistrationResponse{WorkId: id}, nil
}
