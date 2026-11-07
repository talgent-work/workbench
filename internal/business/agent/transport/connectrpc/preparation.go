package connectrpc

import (
	"context"

	agentprivatev1 "github.com/talgent-work/contracts/gen/go/agent/private/v1"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
)

func (h *AgentHandler) GetWorkPreparation(ctx context.Context, request *agentprivatev1.GetWorkPreparationRequest) (*agentprivatev1.GetWorkPreparationResponse, error) {
	if err := authenticateCaller(h.token, requestHeader(ctx).Get("Authorization"), request.GetSubjectId()); err != nil {
		return nil, err
	}
	value, found, err := h.application.Queries.GetWorkPreparation.Handle(ctx, query.GetWork{SubjectID: request.GetSubjectId(), WorkID: request.GetWorkId()})
	if err != nil {
		return nil, mapAgentError(err)
	}
	response := &agentprivatev1.GetWorkPreparationResponse{}
	if found {
		response.Preparation = preparationToProto(value)
	}
	return response, nil
}

func (h *AgentHandler) ListSubmittedPreparations(ctx context.Context, request *agentprivatev1.ListSubmittedPreparationsRequest) (*agentprivatev1.ListSubmittedPreparationsResponse, error) {
	if err := authenticateIntegration(h.token, requestHeader(ctx).Get("Authorization")); err != nil {
		return nil, err
	}
	values, err := h.application.Queries.ListSubmittedPreparations.Handle(ctx, request.GetAfterWorkId())
	if err != nil {
		return nil, mapAgentError(err)
	}
	response := &agentprivatev1.ListSubmittedPreparationsResponse{}
	for _, value := range values {
		response.Preparations = append(response.Preparations, preparationToProto(value))
	}
	return response, nil
}

func preparationToProto(value query.WorkPreparation) *agentprivatev1.WorkPreparation {
	result := &agentprivatev1.WorkPreparation{
		WorkId: value.WorkID, RequestId: value.RequestID, SubjectId: value.SubjectID,
		WorkBranch: value.WorkBranch, InputId: value.InputID, Input: value.Input,
		Submitted: value.Submitted, InputPrepared: value.InputPrepared, InputBuffered: value.InputBuffered,
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
