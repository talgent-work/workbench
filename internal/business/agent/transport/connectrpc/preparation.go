package connectrpc

import (
	"context"

	"connectrpc.com/connect"
	agentprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/agent/private/v1"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
)

func (h *AgentHandler) GetWorkPreparation(ctx context.Context, request *connect.Request[agentprivatev1.GetWorkPreparationRequest]) (*connect.Response[agentprivatev1.GetWorkPreparationResponse], error) {
	if err := authenticateCaller(h.token, request.Header().Get("Authorization"), request.Msg.GetSubjectId()); err != nil {
		return nil, err
	}
	value, found, err := h.application.Queries.GetWorkPreparation.Handle(ctx, query.GetWork{SubjectID: request.Msg.GetSubjectId(), WorkID: request.Msg.GetWorkId()})
	if err != nil {
		return nil, mapAgentError(err)
	}
	response := &agentprivatev1.GetWorkPreparationResponse{}
	if found {
		response.Preparation = preparationToProto(value)
	}
	return connect.NewResponse(response), nil
}

func (h *AgentHandler) ListSubmittedPreparations(ctx context.Context, request *connect.Request[agentprivatev1.ListSubmittedPreparationsRequest]) (*connect.Response[agentprivatev1.ListSubmittedPreparationsResponse], error) {
	if err := authenticateIntegration(h.token, request.Header().Get("Authorization")); err != nil {
		return nil, err
	}
	values, err := h.application.Queries.ListSubmittedPreparations.Handle(ctx, request.Msg.GetAfterWorkId())
	if err != nil {
		return nil, mapAgentError(err)
	}
	response := &agentprivatev1.ListSubmittedPreparationsResponse{}
	for _, value := range values {
		response.Preparations = append(response.Preparations, preparationToProto(value))
	}
	return connect.NewResponse(response), nil
}

func preparationToProto(value query.WorkPreparation) *agentprivatev1.WorkPreparation {
	result := &agentprivatev1.WorkPreparation{
		WorkId: value.WorkID, RequestId: value.RequestID, SubjectId: value.SubjectID,
		WorkBranch: value.WorkBranch, InputId: value.InputID, Input: value.Input,
		Submitted: value.Submitted, InputPrepared: value.InputPrepared, InputBuffered: value.InputBuffered, Rejected: value.Rejected,
	}
	for _, checkout := range value.Checkouts {
		result.Checkouts = append(result.Checkouts, &agentprivatev1.PreparedCheckout{
			Provider: checkout.Provider, RepoId: checkout.RepoID,
			RepoName: checkout.RepoName, CloneUrl: checkout.CloneURL, WebUrl: checkout.WebURL,
			DefaultBranch: checkout.DefaultBranch, BaseBranch: checkout.BaseBranch, WorkBranch: checkout.WorkBranch,
			TargetPath: checkout.TargetPath, CredentialGrantId: checkout.AccessID,
		})
	}
	return result
}
