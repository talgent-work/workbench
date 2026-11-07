package connectrpc

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/types/known/timestamppb"

	agentpublicv1 "github.com/talgent-work/contracts/gen/go/agent/public/v1"
	"github.com/talgent-work/contracts/gen/go/agent/public/v1/agentpublicv1connect"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/personalwork"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/pkg/authn"
	"github.com/talgent-work/workbench/internal/pkg/pluginconfig"
)

// PersonalWorkHandler serves a person's own Works for the gateway-asserted
// person; no request names whom it acts for.
type PersonalWorkHandler struct{ works *personalwork.PersonalWorks }

var _ agentpublicv1connect.PersonalWorkServiceHandler = (*PersonalWorkHandler)(nil)

func NewPersonalWorkHandler(works *personalwork.PersonalWorks) *PersonalWorkHandler {
	return &PersonalWorkHandler{works: works}
}

// currentPerson is the gateway-asserted person, or the owner of the Work
// whose verified native process asks; the application decides what each may
// read. An absent person is refused by the application.
func currentPerson(ctx context.Context) personalwork.Person {
	user, _ := authn.CurrentUser(ctx)
	person := personalwork.Person{UserID: user.ID, DisplayName: user.DisplayName}
	if process, native := authn.NativeProcessFromContext(ctx); native {
		person.Agent = &personalwork.ActingAgent{ProjectID: process.ProjectID, Purpose: process.Purpose}
	}
	return person
}

func (h *PersonalWorkHandler) GetPersonalWorkStartOptions(ctx context.Context, _ *agentpublicv1.GetPersonalWorkStartOptionsRequest) (*agentpublicv1.GetPersonalWorkStartOptionsResponse, error) {
	value, err := h.works.StartOptions(ctx, currentPerson(ctx))
	if err != nil {
		return nil, mapPersonalWorkError(ctx, err)
	}
	return &agentpublicv1.GetPersonalWorkStartOptionsResponse{Options: workStartOptionsToProto(value)}, nil
}

func (h *PersonalWorkHandler) ListPersonalWorkStartRepositories(ctx context.Context, _ *agentpublicv1.ListPersonalWorkStartRepositoriesRequest) (*agentpublicv1.ListPersonalWorkStartRepositoriesResponse, error) {
	value, err := h.works.StartRepositories(ctx, currentPerson(ctx))
	if err != nil {
		return nil, mapPersonalWorkError(ctx, err)
	}
	return &agentpublicv1.ListPersonalWorkStartRepositoriesResponse{Repositories: workStartRepositoriesToProto(value)}, nil
}

func (h *PersonalWorkHandler) StartPersonalWork(ctx context.Context, request *agentpublicv1.StartPersonalWorkRequest) (*agentpublicv1.StartPersonalWorkResponse, error) {
	msg := request
	start := personalwork.StartPersonalWork{
		Person: currentPerson(ctx), RequestID: strings.TrimSpace(msg.GetRequestId()), InitialMessage: msg.GetInitialMessage(),
		Capabilities: creationCapabilitiesFromProto(msg.GetCreationCapabilities()), WorkModel: workModelFromProto(msg.GetModel()),
		ThinkingLevel: strings.TrimSpace(msg.GetThinkingLevel()), ComputerID: strings.TrimSpace(msg.GetComputerId()),
		Harness: harnessChoiceFromProto(msg.GetHarness()),
	}
	for _, selection := range msg.GetRepositorySelections() {
		start.Selections = append(start.Selections, command.RepositorySelection{RepositoryID: selection.GetRepositoryId(), BaseBranch: selection.GetBaseBranch()})
	}
	workID, err := h.works.Start(ctx, start)
	if err != nil {
		return nil, mapPersonalWorkError(ctx, err)
	}
	return &agentpublicv1.StartPersonalWorkResponse{WorkId: workID}, nil
}

func creationCapabilitiesFromProto(value *agentpublicv1.CreationCapabilities) domain.CapabilityDifferences {
	if value == nil {
		return domain.CapabilityDifferences{}
	}
	result := domain.CapabilityDifferences{}
	if value.Prompt != nil {
		prompt := value.GetPrompt()
		result.Prompt = &prompt
	}
	if value.StartupAgent != nil {
		startup := value.GetStartupAgent()
		result.StartupAgent = &startup
	}
	for _, plugin := range value.GetPlugins() {
		result.Plugins = append(result.Plugins, domain.PluginChoice{Slug: plugin.GetName(), Include: plugin.Include == nil || plugin.GetInclude(), Configuration: pluginconfig.FromJSON(plugin.GetConfiguration().AsMap())})
	}
	for _, connector := range value.GetConnectors() {
		result.Connectors = append(result.Connectors, domain.ConnectorChoice{Name: connector.GetName(), Include: connector.GetInclude()})
	}
	return result
}

func (h *PersonalWorkHandler) ListPersonalWorks(ctx context.Context, request *agentpublicv1.ListPersonalWorksRequest) (*agentpublicv1.ListPersonalWorksResponse, error) {
	works, err := h.works.List(ctx, currentPerson(ctx), int(request.GetLimit()))
	if err != nil {
		return nil, mapPersonalWorkError(ctx, err)
	}
	response := &agentpublicv1.ListPersonalWorksResponse{Works: make([]*agentpublicv1.PersonalWorkSummary, 0, len(works))}
	for _, work := range works {
		response.Works = append(response.Works, &agentpublicv1.PersonalWorkSummary{
			WorkId: work.WorkID, Title: work.Title, State: work.State, StateReason: work.StateReason, Waiting: work.Waiting,
			CreatedAt: timestamppb.New(work.CreatedAt), UpdatedAt: timestamppb.New(work.UpdatedAt), DisconnectedSince: optionalTimestamp(work.DisconnectedSince),
		})
	}
	return response, nil
}

func (h *PersonalWorkHandler) ChangePersonalWorkTitle(ctx context.Context, request *agentpublicv1.ChangePersonalWorkTitleRequest) (*agentpublicv1.ChangePersonalWorkTitleResponse, error) {
	title := strings.TrimSpace(request.GetTitle())
	if err := h.works.ChangeTitle(ctx, currentPerson(ctx), request.GetWorkId(), title); err != nil {
		return nil, mapPersonalWorkError(ctx, err)
	}
	return &agentpublicv1.ChangePersonalWorkTitleResponse{Title: title}, nil
}

func (h *PersonalWorkHandler) SearchShareReaders(ctx context.Context, request *agentpublicv1.SearchShareReadersRequest) (*agentpublicv1.SearchShareReadersResponse, error) {
	users, err := h.works.SearchShareReaders(ctx, currentPerson(ctx), request.GetQuery(), int(request.GetLimit()))
	if err != nil {
		return nil, mapPersonalWorkError(ctx, err)
	}
	return &agentpublicv1.SearchShareReadersResponse{Users: directoryUsersToProto(users)}, nil
}

func (h *PersonalWorkHandler) GetShareReaders(ctx context.Context, request *agentpublicv1.GetShareReadersRequest) (*agentpublicv1.GetShareReadersResponse, error) {
	users, err := h.works.GetShareReaders(ctx, currentPerson(ctx), request.GetUserIds())
	if err != nil {
		return nil, mapPersonalWorkError(ctx, err)
	}
	return &agentpublicv1.GetShareReadersResponse{Users: directoryUsersToProto(users)}, nil
}

func directoryUsersToProto(users []personalwork.DirectoryUser) []*agentpublicv1.DirectoryUser {
	out := make([]*agentpublicv1.DirectoryUser, 0, len(users))
	for _, user := range users {
		out = append(out, &agentpublicv1.DirectoryUser{UserId: user.UserID, Username: user.Username, DisplayName: user.DisplayName, AvatarUrl: user.AvatarURL})
	}
	return out
}

func (h *PersonalWorkHandler) GrantPersonalWorkShare(ctx context.Context, request *agentpublicv1.GrantPersonalWorkShareRequest) (*agentpublicv1.GrantPersonalWorkShareResponse, error) {
	if err := h.works.GrantShare(ctx, currentPerson(ctx), request.GetWorkId(), request.GetReaderUserId()); err != nil {
		return nil, mapPersonalWorkError(ctx, err)
	}
	return &agentpublicv1.GrantPersonalWorkShareResponse{}, nil
}

func (h *PersonalWorkHandler) ListLibraryFiles(ctx context.Context, request *agentpublicv1.ListLibraryFilesRequest) (*agentpublicv1.ListLibraryFilesResponse, error) {
	msg := request
	filter := personalwork.LibraryFilter{NameQuery: msg.GetNameQuery(), WorkID: strings.TrimSpace(msg.GetWorkId())}
	if msg.AddedAfter != nil {
		filter.AddedAfter = msg.GetAddedAfter().AsTime()
	}
	if msg.AddedBefore != nil {
		filter.AddedBefore = msg.GetAddedBefore().AsTime()
	}
	library, err := h.works.Library(ctx, currentPerson(ctx), filter)
	if err != nil {
		return nil, mapPersonalWorkError(ctx, err)
	}
	response := &agentpublicv1.ListLibraryFilesResponse{
		Files: make([]*agentpublicv1.LibraryFile, 0, len(library.Files)), Works: make([]*agentpublicv1.LibraryWork, 0, len(library.Works)),
	}
	for _, item := range library.Files {
		response.Files = append(response.Files, &agentpublicv1.LibraryFile{
			WorkId: item.Work.WorkID, WorkTitle: item.Work.Title, Name: item.File.Name, SizeBytes: item.File.SizeBytes,
			ContentType: item.File.ContentType, AddedAt: timestamppb.New(item.File.UpdatedAt), SharedByUserId: item.Work.SharedBy,
		})
	}
	for _, work := range library.Works {
		response.Works = append(response.Works, &agentpublicv1.LibraryWork{WorkId: work.WorkID, Title: work.Title, SharedByUserId: work.SharedBy, Personal: work.Personal})
	}
	return response, nil
}

// errUnavailable is what a person sees of a failure that is not theirs to
// correct; its cause is logged, never sent to the browser.
var errUnavailable = errors.New("agent: the request could not be completed")

// mapPersonalWorkError maps a person's own-Work outcomes, then Agent's.
func mapPersonalWorkError(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, personalwork.ErrInvalidRequest), errors.Is(err, personalwork.ErrReaderUnknown), errors.Is(err, personalwork.ErrInvalidFileName):
		return connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	case errors.Is(err, personalwork.ErrFileNotFound):
		return connect.NewError(connect.CodeNotFound, err.Error()).WithCause(err)
	case errors.Is(err, personalwork.ErrFileNameTaken):
		return connect.NewError(connect.CodeAlreadyExists, err.Error()).WithCause(err)
	}
	mapped := mapAgentError(err)
	if code := connect.CodeOf(mapped); code == connect.CodeInternal || code == connect.CodeUnknown {
		slog.ErrorContext(ctx, "personal work request failed", slog.Any("error", err))
		return connect.NewError(code, errUnavailable.Error()).WithCause(errUnavailable)
	}
	return mapped
}
