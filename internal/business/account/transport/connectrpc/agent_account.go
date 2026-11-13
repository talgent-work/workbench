package connectrpc

import (
	"context"
	"errors"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/types/known/timestamppb"

	accountprivatev1 "github.com/talgent-work/contracts/gen/go/account/private/v1"
	"github.com/talgent-work/contracts/gen/go/account/private/v1/accountprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/account/application"
	"github.com/talgent-work/workbench/internal/business/account/domain"
)

// AgentAccounts issues Works' agent Identity Tokens to Agent, a trusted
// in-cluster caller; it is never mounted on the public gateway.
type AgentAccounts struct {
	app *application.Application
}

var _ accountprivatev1connect.AgentAccountServiceHandler = (*AgentAccounts)(nil)

func NewAgentAccounts(app *application.Application) *AgentAccounts {
	return &AgentAccounts{app: app}
}

func (h *AgentAccounts) IssueWorkIdentityToken(ctx context.Context, req *accountprivatev1.IssueWorkIdentityTokenRequest) (*accountprivatev1.IssueWorkIdentityTokenResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	if err := requireService(info.RequestHeader()); err != nil {
		return nil, err
	}
	// The Account still holds a Project and a Subject: a Work Caller of kind
	// "project" maps to its Project and the Work Key to its Subject until the
	// Agent Account records the Work Caller, Work Key and File Home.
	projectID := ""
	if req.GetWorkCallerKind() == "project" {
		projectID = req.GetWorkCallerId()
	}
	issued, err := h.app.IssueWorkIdentityToken(ctx, application.IssueWorkIdentityToken{
		WorkID:            req.GetWorkId(),
		ProjectID:         projectID,
		AgentID:           req.GetAgentId(),
		Purpose:           req.GetPurpose(),
		SubjectKind:       req.GetWorkKeyKind(),
		SubjectID:         req.GetWorkKeyId(),
		RepresentedPerson: req.GetRepresentedUserId(),
		OwningApplication: req.GetApplicationId(),
		WorkLabels:        req.GetWorkLabels(),
	})
	switch {
	case errors.Is(err, domain.ErrInvalidAgentAccount):
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error())
	case errors.Is(err, domain.ErrAgentAccountFactsDiffer):
		return nil, connect.NewError(connect.CodeFailedPrecondition, err.Error())
	case err != nil:
		// The cause may name internals; the token never reaches an error.
		return nil, connect.NewError(connect.CodeInternal, "agent identity token not issued").WithCause(err)
	}
	return &accountprivatev1.IssueWorkIdentityTokenResponse{Token: issued.Token, ExpiresAt: timestamppb.New(issued.ExpiresAt)}, nil
}
