package connectrpc

import (
	"context"

	"connectrpc.com/connect/v2"

	agentpublicv1 "github.com/talgent-work/contracts/gen/go/agent/public/v1"
	"github.com/talgent-work/contracts/gen/go/agent/public/v1/agentpublicv1connect"
	"github.com/talgent-work/workbench/internal/pkg/authn"
)

// IdentityHandler tells a Work's Agent who it is from its Identity Token's
// fixed facts; it reads nothing else.
type IdentityHandler struct{}

var _ agentpublicv1connect.IdentityServiceHandler = IdentityHandler{}

func (IdentityHandler) WhoAmI(ctx context.Context, _ *agentpublicv1.WhoAmIRequest) (*agentpublicv1.WhoAmIResponse, error) {
	agent, ok := authn.CurrentAgent(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodePermissionDenied, "agent: only a Work's Agent asks who it is")
	}
	return &agentpublicv1.WhoAmIResponse{
		WorkId: agent.WorkID, ProjectId: agent.ProjectID, SubjectKind: agent.SubjectKind, SubjectId: agent.SubjectID,
		Purpose: agent.Purpose, AgentId: agent.AgentID,
	}, nil
}
