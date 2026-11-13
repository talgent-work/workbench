package connectrpc

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/types/known/timestamppb"

	agentpublicv1 "github.com/talgent-work/contracts/gen/go/agent/public/v1"
	agentpublicv2 "github.com/talgent-work/contracts/gen/go/agent/public/v2"
	"github.com/talgent-work/contracts/gen/go/agent/public/v2/agentpublicv2connect"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/standalonework"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/pkg/authn"
	"github.com/talgent-work/workbench/internal/pkg/pluginconfig"
)

// StandaloneWorkHandler serves an owner's Standalone Works for the
// gateway-asserted caller; no request names whom it acts for.
type StandaloneWorkHandler struct {
	works *standalonework.StandaloneWorks
}

var _ agentpublicv2connect.WorkServiceHandler = (*StandaloneWorkHandler)(nil)

func NewStandaloneWorkHandler(works *standalonework.StandaloneWorks) *StandaloneWorkHandler {
	return &StandaloneWorkHandler{works: works}
}

// currentCaller maps the request's Principal to whom Standalone Works act
// for: a user or an Application asks for itself; an agent asks on behalf of
// the person its Work represents, from its Work's Project and purpose, and
// the application decides what that Agent may read. An absent owner is
// refused by the application.
func currentCaller(ctx context.Context) standalonework.Caller {
	principal, _ := authn.PrincipalFrom(ctx)
	if agent, ok := principal.Agent(); ok {
		// Any Work Caller but a person stands where the Work's Project stood,
		// so such a Work's Agent reads no Personal Library.
		scope := ""
		if agent.WorkCaller.Kind != "person" {
			scope = agent.WorkCaller.Kind + ":" + agent.WorkCaller.ID
		}
		return standalonework.Caller{UserID: agent.RepresentedUserID, Agent: &standalonework.ActingAgent{ProjectID: scope, Purpose: agent.Purpose}}
	}
	if application, ok := principal.Application(); ok {
		return standalonework.Caller{ApplicationID: application.ID}
	}
	user, _ := authn.CurrentUser(ctx)
	return standalonework.Caller{UserID: user.ID, DisplayName: user.DisplayName}
}

// currentOwner is the owner a Work operation acts for: the person or the
// Application the request speaks for; empty for an Agent, which owns no
// Work.
func currentOwner(ctx context.Context) string { return currentCaller(ctx).Owner() }

func (h *StandaloneWorkHandler) GetWorkStartOptions(ctx context.Context, _ *agentpublicv2.GetWorkStartOptionsRequest) (*agentpublicv2.GetWorkStartOptionsResponse, error) {
	value, err := h.works.StartOptions(ctx, currentCaller(ctx))
	if err != nil {
		return nil, mapStandaloneWorkError(ctx, err)
	}
	return &agentpublicv2.GetWorkStartOptionsResponse{Options: workStartOptionsToProto(value)}, nil
}

func (h *StandaloneWorkHandler) ListWorkStartRepositories(ctx context.Context, _ *agentpublicv2.ListWorkStartRepositoriesRequest) (*agentpublicv2.ListWorkStartRepositoriesResponse, error) {
	value, err := h.works.StartRepositories(ctx, currentCaller(ctx))
	if err != nil {
		return nil, mapStandaloneWorkError(ctx, err)
	}
	return &agentpublicv2.ListWorkStartRepositoriesResponse{Repositories: workStartRepositoriesToProto(value)}, nil
}

func (h *StandaloneWorkHandler) StartWork(ctx context.Context, request *agentpublicv2.StartWorkRequest) (*agentpublicv2.StartWorkResponse, error) {
	msg := request
	start := standalonework.StartWork{
		Caller: currentCaller(ctx), RequestID: strings.TrimSpace(msg.GetRequestId()), InitialMessage: msg.GetInitialMessage(),
		Capabilities: creationCapabilitiesFromProto(msg.GetCreationCapabilities()), WorkModel: workModelFromProto(msg.GetModel()),
		ThinkingLevel: strings.TrimSpace(msg.GetThinkingLevel()), ComputerID: strings.TrimSpace(msg.GetComputerId()),
		Harness: harnessChoiceFromProto(msg.GetHarness()), Labels: msg.GetLabels(),
	}
	for _, endpoint := range msg.GetApplicationMcpEndpoints() {
		start.Capabilities.ApplicationEndpoints = append(start.Capabilities.ApplicationEndpoints, domain.ApplicationMCPEndpoint{
			Name: strings.TrimSpace(endpoint.GetName()), URL: strings.TrimSpace(endpoint.GetUrl()), Headers: endpoint.GetHeaders(),
		})
	}
	for _, selection := range msg.GetRepositorySelections() {
		start.Selections = append(start.Selections, command.RepositorySelection{RepositoryID: selection.GetRepositoryId(), BaseBranch: selection.GetBaseBranch()})
	}
	workID, err := h.works.Start(ctx, start)
	if err != nil {
		return nil, mapStandaloneWorkError(ctx, err)
	}
	return &agentpublicv2.StartWorkResponse{WorkId: workID}, nil
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

func (h *StandaloneWorkHandler) ListWorks(ctx context.Context, request *agentpublicv2.ListWorksRequest) (*agentpublicv2.ListWorksResponse, error) {
	works, err := h.works.List(ctx, standalonework.ListWorks{Caller: currentCaller(ctx), Application: request.GetApplicationId(), Limit: int(request.GetLimit()), Labels: request.GetLabels(), States: request.GetStates()})
	if err != nil {
		return nil, mapStandaloneWorkError(ctx, err)
	}
	response := &agentpublicv2.ListWorksResponse{Works: make([]*agentpublicv2.WorkSummary, 0, len(works))}
	for _, work := range works {
		response.Works = append(response.Works, &agentpublicv2.WorkSummary{
			WorkId: work.WorkID, Title: work.Title, Labels: work.Labels, State: work.State, StateReason: work.StateReason, Waiting: work.Waiting,
			CreatedAt: timestamppb.New(work.CreatedAt), UpdatedAt: timestamppb.New(work.UpdatedAt), DisconnectedSince: optionalTimestamp(work.DisconnectedSince),
		})
	}
	return response, nil
}

func (h *StandaloneWorkHandler) ChangeWorkTitle(ctx context.Context, request *agentpublicv2.ChangeWorkTitleRequest) (*agentpublicv2.ChangeWorkTitleResponse, error) {
	title := strings.TrimSpace(request.GetTitle())
	if err := h.works.ChangeTitle(ctx, currentCaller(ctx), request.GetWorkId(), title); err != nil {
		return nil, mapStandaloneWorkError(ctx, err)
	}
	return &agentpublicv2.ChangeWorkTitleResponse{Title: title}, nil
}

func (h *StandaloneWorkHandler) SearchShareReaders(ctx context.Context, request *agentpublicv2.SearchShareReadersRequest) (*agentpublicv2.SearchShareReadersResponse, error) {
	users, err := h.works.SearchShareReaders(ctx, currentCaller(ctx), request.GetQuery(), int(request.GetLimit()))
	if err != nil {
		return nil, mapStandaloneWorkError(ctx, err)
	}
	return &agentpublicv2.SearchShareReadersResponse{Users: directoryUsersToProto(users)}, nil
}

func (h *StandaloneWorkHandler) GetShareReaders(ctx context.Context, request *agentpublicv2.GetShareReadersRequest) (*agentpublicv2.GetShareReadersResponse, error) {
	users, err := h.works.GetShareReaders(ctx, currentCaller(ctx), request.GetUserIds())
	if err != nil {
		return nil, mapStandaloneWorkError(ctx, err)
	}
	return &agentpublicv2.GetShareReadersResponse{Users: directoryUsersToProto(users)}, nil
}

func directoryUsersToProto(users []command.DirectoryUser) []*agentpublicv2.DirectoryUser {
	out := make([]*agentpublicv2.DirectoryUser, 0, len(users))
	for _, user := range users {
		out = append(out, &agentpublicv2.DirectoryUser{UserId: user.UserID, Username: user.Username, DisplayName: user.DisplayName, AvatarUrl: user.AvatarURL})
	}
	return out
}

func (h *StandaloneWorkHandler) GrantWorkShare(ctx context.Context, request *agentpublicv2.GrantWorkShareRequest) (*agentpublicv2.GrantWorkShareResponse, error) {
	if err := h.works.GrantShare(ctx, currentCaller(ctx), request.GetWorkId(), request.GetReaderUserId()); err != nil {
		return nil, mapStandaloneWorkError(ctx, err)
	}
	return &agentpublicv2.GrantWorkShareResponse{}, nil
}

func (h *StandaloneWorkHandler) ListLibraryFiles(ctx context.Context, request *agentpublicv2.ListLibraryFilesRequest) (*agentpublicv2.ListLibraryFilesResponse, error) {
	msg := request
	filter := standalonework.LibraryFilter{NameQuery: msg.GetNameQuery(), WorkID: strings.TrimSpace(msg.GetWorkId())}
	if msg.AddedAfter != nil {
		filter.AddedAfter = msg.GetAddedAfter().AsTime()
	}
	if msg.AddedBefore != nil {
		filter.AddedBefore = msg.GetAddedBefore().AsTime()
	}
	library, err := h.works.Library(ctx, currentCaller(ctx), filter)
	if err != nil {
		return nil, mapStandaloneWorkError(ctx, err)
	}
	response := &agentpublicv2.ListLibraryFilesResponse{
		Files: make([]*agentpublicv2.LibraryFile, 0, len(library.Files)), Works: make([]*agentpublicv2.LibraryWork, 0, len(library.Works)),
	}
	for _, item := range library.Files {
		response.Files = append(response.Files, &agentpublicv2.LibraryFile{
			WorkId: item.Work.WorkID, WorkTitle: item.Work.Title, Name: item.File.Name, SizeBytes: item.File.SizeBytes,
			ContentType: item.File.ContentType, AddedAt: timestamppb.New(item.File.UpdatedAt), SharedByUserId: item.Work.SharedBy,
		})
	}
	for _, work := range library.Works {
		response.Works = append(response.Works, &agentpublicv2.LibraryWork{WorkId: work.WorkID, Title: work.Title, SharedByUserId: work.SharedBy, Standalone: work.Standalone})
	}
	return response, nil
}

// errUnavailable is what a person sees of a failure that is not theirs to
// correct; its cause is logged, never sent to the browser.
var errUnavailable = errors.New("agent: the request could not be completed")

// mapStandaloneWorkError maps a person's own-Work outcomes, then Agent's.
func mapStandaloneWorkError(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, standalonework.ErrInvalidRequest), errors.Is(err, standalonework.ErrReaderUnknown), errors.Is(err, command.ErrInvalidFileName):
		return connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	case errors.Is(err, command.ErrFileNotFound):
		return connect.NewError(connect.CodeNotFound, err.Error()).WithCause(err)
	case errors.Is(err, command.ErrFileNameTaken):
		return connect.NewError(connect.CodeAlreadyExists, err.Error()).WithCause(err)
	}
	mapped := mapAgentError(err)
	if code := connect.CodeOf(mapped); code == connect.CodeInternal || code == connect.CodeUnknown {
		slog.ErrorContext(ctx, "standalone work request failed", slog.Any("error", err))
		return connect.NewError(code, errUnavailable.Error()).WithCause(errUnavailable)
	}
	return mapped
}
