package connectrpc

import (
	"context"

	agentpublicv1 "github.com/talgent-work/contracts/gen/go/agent/public/v1"
	"github.com/talgent-work/contracts/gen/go/agent/public/v1/agentpublicv1connect"
	agentpublicv2 "github.com/talgent-work/contracts/gen/go/agent/public/v2"
)

// PersonalWorkHandler serves the retiring agent.public.v1 PersonalWorkService
// by translating each call to agent.public.v2 WorkService, which owns the
// behavior. Remove it with the contract once no client calls it: a web app
// built on an earlier workbench-ui release still does.
type PersonalWorkHandler struct{ works *StandaloneWorkHandler }

var _ agentpublicv1connect.PersonalWorkServiceHandler = (*PersonalWorkHandler)(nil)

func NewPersonalWorkHandler(works *StandaloneWorkHandler) *PersonalWorkHandler {
	return &PersonalWorkHandler{works: works}
}

func (h *PersonalWorkHandler) GetPersonalWorkStartOptions(ctx context.Context, _ *agentpublicv1.GetPersonalWorkStartOptionsRequest) (*agentpublicv1.GetPersonalWorkStartOptionsResponse, error) {
	response, err := h.works.GetWorkStartOptions(ctx, &agentpublicv2.GetWorkStartOptionsRequest{})
	if err != nil {
		return nil, err
	}
	return &agentpublicv1.GetPersonalWorkStartOptionsResponse{Options: response.GetOptions()}, nil
}

func (h *PersonalWorkHandler) ListPersonalWorkStartRepositories(ctx context.Context, _ *agentpublicv1.ListPersonalWorkStartRepositoriesRequest) (*agentpublicv1.ListPersonalWorkStartRepositoriesResponse, error) {
	response, err := h.works.ListWorkStartRepositories(ctx, &agentpublicv2.ListWorkStartRepositoriesRequest{})
	if err != nil {
		return nil, err
	}
	return &agentpublicv1.ListPersonalWorkStartRepositoriesResponse{Repositories: response.GetRepositories()}, nil
}

func (h *PersonalWorkHandler) StartPersonalWork(ctx context.Context, request *agentpublicv1.StartPersonalWorkRequest) (*agentpublicv1.StartPersonalWorkResponse, error) {
	response, err := h.works.StartWork(ctx, &agentpublicv2.StartWorkRequest{
		RequestId: request.GetRequestId(), InitialMessage: request.GetInitialMessage(), RepositorySelections: request.GetRepositorySelections(),
		CreationCapabilities: request.GetCreationCapabilities(), Model: request.GetModel(), ThinkingLevel: request.GetThinkingLevel(),
		ComputerId: request.GetComputerId(), Harness: request.GetHarness(),
	})
	if err != nil {
		return nil, err
	}
	return &agentpublicv1.StartPersonalWorkResponse{WorkId: response.GetWorkId()}, nil
}

func (h *PersonalWorkHandler) ListPersonalWorks(ctx context.Context, request *agentpublicv1.ListPersonalWorksRequest) (*agentpublicv1.ListPersonalWorksResponse, error) {
	response, err := h.works.ListWorks(ctx, &agentpublicv2.ListWorksRequest{Limit: request.GetLimit()})
	if err != nil {
		return nil, err
	}
	works := make([]*agentpublicv1.PersonalWorkSummary, 0, len(response.GetWorks()))
	for _, work := range response.GetWorks() {
		works = append(works, &agentpublicv1.PersonalWorkSummary{
			WorkId: work.GetWorkId(), Title: work.GetTitle(), State: work.GetState(), Waiting: work.GetWaiting(), CreatedAt: work.GetCreatedAt(),
			UpdatedAt: work.GetUpdatedAt(), DisconnectedSince: work.GetDisconnectedSince(), StateReason: work.GetStateReason(),
		})
	}
	return &agentpublicv1.ListPersonalWorksResponse{Works: works}, nil
}

func (h *PersonalWorkHandler) ChangePersonalWorkTitle(ctx context.Context, request *agentpublicv1.ChangePersonalWorkTitleRequest) (*agentpublicv1.ChangePersonalWorkTitleResponse, error) {
	response, err := h.works.ChangeWorkTitle(ctx, &agentpublicv2.ChangeWorkTitleRequest{WorkId: request.GetWorkId(), Title: request.GetTitle()})
	if err != nil {
		return nil, err
	}
	return &agentpublicv1.ChangePersonalWorkTitleResponse{Title: response.GetTitle()}, nil
}

func (h *PersonalWorkHandler) SearchShareReaders(ctx context.Context, request *agentpublicv1.SearchShareReadersRequest) (*agentpublicv1.SearchShareReadersResponse, error) {
	response, err := h.works.SearchShareReaders(ctx, &agentpublicv2.SearchShareReadersRequest{Query: request.GetQuery(), Limit: request.GetLimit()})
	if err != nil {
		return nil, err
	}
	return &agentpublicv1.SearchShareReadersResponse{Users: v1DirectoryUsers(response.GetUsers())}, nil
}

func (h *PersonalWorkHandler) GetShareReaders(ctx context.Context, request *agentpublicv1.GetShareReadersRequest) (*agentpublicv1.GetShareReadersResponse, error) {
	response, err := h.works.GetShareReaders(ctx, &agentpublicv2.GetShareReadersRequest{UserIds: request.GetUserIds()})
	if err != nil {
		return nil, err
	}
	return &agentpublicv1.GetShareReadersResponse{Users: v1DirectoryUsers(response.GetUsers())}, nil
}

func v1DirectoryUsers(users []*agentpublicv2.DirectoryUser) []*agentpublicv1.DirectoryUser {
	out := make([]*agentpublicv1.DirectoryUser, 0, len(users))
	for _, user := range users {
		out = append(out, &agentpublicv1.DirectoryUser{UserId: user.GetUserId(), Username: user.GetUsername(), DisplayName: user.GetDisplayName(), AvatarUrl: user.GetAvatarUrl()})
	}
	return out
}

func (h *PersonalWorkHandler) GrantPersonalWorkShare(ctx context.Context, request *agentpublicv1.GrantPersonalWorkShareRequest) (*agentpublicv1.GrantPersonalWorkShareResponse, error) {
	if _, err := h.works.GrantWorkShare(ctx, &agentpublicv2.GrantWorkShareRequest{WorkId: request.GetWorkId(), ReaderUserId: request.GetReaderUserId()}); err != nil {
		return nil, err
	}
	return &agentpublicv1.GrantPersonalWorkShareResponse{}, nil
}

func (h *PersonalWorkHandler) ListLibraryFiles(ctx context.Context, request *agentpublicv1.ListLibraryFilesRequest) (*agentpublicv1.ListLibraryFilesResponse, error) {
	response, err := h.works.ListLibraryFiles(ctx, &agentpublicv2.ListLibraryFilesRequest{
		NameQuery: request.GetNameQuery(), WorkId: request.GetWorkId(), AddedAfter: request.GetAddedAfter(), AddedBefore: request.GetAddedBefore(),
	})
	if err != nil {
		return nil, err
	}
	library := &agentpublicv1.ListLibraryFilesResponse{
		Files: make([]*agentpublicv1.LibraryFile, 0, len(response.GetFiles())), Works: make([]*agentpublicv1.LibraryWork, 0, len(response.GetWorks())),
	}
	for _, file := range response.GetFiles() {
		library.Files = append(library.Files, &agentpublicv1.LibraryFile{
			WorkId: file.GetWorkId(), WorkTitle: file.GetWorkTitle(), Name: file.GetName(), SizeBytes: file.GetSizeBytes(),
			ContentType: file.GetContentType(), AddedAt: file.GetAddedAt(), SharedByUserId: file.GetSharedByUserId(),
		})
	}
	for _, work := range response.GetWorks() {
		library.Works = append(library.Works, &agentpublicv1.LibraryWork{WorkId: work.GetWorkId(), Title: work.GetTitle(), SharedByUserId: work.GetSharedByUserId(), Personal: work.GetStandalone()})
	}
	return library, nil
}
