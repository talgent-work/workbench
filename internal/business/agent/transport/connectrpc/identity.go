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
		WorkId: agent.WorkID, WorkCaller: identityWorkCaller(agent), WorkKey: identityWorkKey(agent),
		Purpose: agent.Purpose, AgentId: agent.AgentID,
	}, nil
}

// identityWorkCaller and identityWorkKey state the Work's facts from the
// identity headers the entry still writes, which name a Project and a Work
// Subject: a stopgap until the entry writes the Work Caller, Work Key and File
// Home themselves. A Work without a Project is answered without a Work Caller.
func identityWorkCaller(agent authn.Agent) *agentpublicv1.WorkCaller {
	if agent.ProjectID == "" {
		return nil
	}
	return &agentpublicv1.WorkCaller{Kind: "project", Id: agent.ProjectID}
}

func identityWorkKey(agent authn.Agent) *agentpublicv1.WorkKey {
	if agent.SubjectKind == "" {
		return nil
	}
	return &agentpublicv1.WorkKey{Kind: agent.SubjectKind, Id: agent.SubjectID}
}
