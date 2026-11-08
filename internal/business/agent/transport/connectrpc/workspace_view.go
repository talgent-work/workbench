package connectrpc

import (
	"context"
	"errors"
	"strings"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/types/known/timestamppb"

	agentprivatev1 "github.com/talgent-work/contracts/gen/go/agent/private/v1"
	"github.com/talgent-work/contracts/gen/go/agent/private/v1/agentprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// WorkspaceReadingHandler serves a trusted caller reading a Work's
// workspace on a reader's behalf: the caller states the reader's Project
// membership, and Agent decides Work access and closure itself.
type WorkspaceReadingHandler struct {
	application *application.Application
	token       string
}

var _ agentprivatev1connect.WorkspaceReadingServiceHandler = (*WorkspaceReadingHandler)(nil)

func NewWorkspaceReadingHandler(app *application.Application, token string) *WorkspaceReadingHandler {
	return &WorkspaceReadingHandler{application: app, token: token}
}

func readWorkspace(workID, readerID, path string, admission *agentprivatev1.ReaderAdmission) application.ReadWorkspace {
	return application.ReadWorkspace{ActorUserID: strings.TrimSpace(readerID), WorkID: strings.TrimSpace(workID), Path: path, Admission: readerAdmissionFromProto(admission)}
}

func (h *WorkspaceReadingHandler) BrowseWorkspace(ctx context.Context, request *agentprivatev1.BrowseWorkspaceRequest) (*agentprivatev1.BrowseWorkspaceResponse, error) {
	if err := authenticateIntegration(h.token, requestHeader(ctx).Get("Authorization")); err != nil {
		return nil, err
	}
	msg := request
	listing, err := h.application.BrowseWorkspace(ctx, readWorkspace(msg.GetWorkId(), msg.GetReaderId(), msg.GetPath(), msg.GetAdmission()))
	if state, unavailable := workspaceState(err); unavailable {
		return &agentprivatev1.BrowseWorkspaceResponse{State: state}, nil
	}
	if err != nil {
		return nil, mapWorkspaceError(err)
	}
	response := &agentprivatev1.BrowseWorkspaceResponse{State: agentprivatev1.WorkspaceState_WORKSPACE_STATE_AVAILABLE, Total: uint32(listing.Total), Entries: make([]*agentprivatev1.WorkspaceEntry, 0, len(listing.Entries))}
	for _, entry := range listing.Entries {
		response.Entries = append(response.Entries, &agentprivatev1.WorkspaceEntry{
			Name: entry.Name, Kind: workspaceEntryKinds[entry.Kind], SizeBytes: entry.SizeBytes,
			ModifiedAt: timestamppb.New(entry.ModifiedAt), AttachmentName: entry.AttachmentName,
		})
	}
	return response, nil
}

func (h *WorkspaceReadingHandler) PreviewWorkspaceFile(ctx context.Context, request *agentprivatev1.PreviewWorkspaceFileRequest) (*agentprivatev1.PreviewWorkspaceFileResponse, error) {
	if err := authenticateIntegration(h.token, requestHeader(ctx).Get("Authorization")); err != nil {
		return nil, err
	}
	msg := request
	preview, err := h.application.PreviewWorkspaceFile(ctx, readWorkspace(msg.GetWorkId(), msg.GetReaderId(), msg.GetPath(), msg.GetAdmission()))
	if state, unavailable := workspaceState(err); unavailable {
		return &agentprivatev1.PreviewWorkspaceFileResponse{State: state}, nil
	}
	if err != nil {
		return nil, mapWorkspaceError(err)
	}
	response := &agentprivatev1.PreviewWorkspaceFileResponse{
		State: agentprivatev1.WorkspaceState_WORKSPACE_STATE_AVAILABLE, Mode: workspacePreviewModes[preview.Mode], Display: workspacePreviewDisplays[preview.Display],
		Refusal: workspacePreviewRefusals[preview.Refusal], LimitBytes: preview.LimitBytes,
	}
	if preview.Link != nil {
		response.AccessUrl, response.ExpiresAt = preview.Link.URL, timestamppb.New(preview.Link.ExpiresAt)
	}
	return response, nil
}

func (h *WorkspaceReadingHandler) DownloadWorkspaceFile(ctx context.Context, request *agentprivatev1.DownloadWorkspaceFileRequest) (*agentprivatev1.DownloadWorkspaceFileResponse, error) {
	if err := authenticateIntegration(h.token, requestHeader(ctx).Get("Authorization")); err != nil {
		return nil, err
	}
	msg := request
	link, err := h.application.DownloadWorkspaceFile(ctx, readWorkspace(msg.GetWorkId(), msg.GetReaderId(), msg.GetPath(), msg.GetAdmission()))
	if state, unavailable := workspaceState(err); unavailable {
		return &agentprivatev1.DownloadWorkspaceFileResponse{State: state}, nil
	}
	if err != nil {
		return nil, mapWorkspaceError(err)
	}
	return &agentprivatev1.DownloadWorkspaceFileResponse{State: agentprivatev1.WorkspaceState_WORKSPACE_STATE_AVAILABLE, AccessUrl: link.URL, ExpiresAt: timestamppb.New(link.ExpiresAt)}, nil
}

var workspacePreviewModes = map[application.WorkspacePreviewMode]agentprivatev1.WorkspacePreviewMode{
	application.WorkspacePreviewRefused:   agentprivatev1.WorkspacePreviewMode_WORKSPACE_PREVIEW_MODE_REFUSED,
	application.WorkspacePreviewDirect:    agentprivatev1.WorkspacePreviewMode_WORKSPACE_PREVIEW_MODE_DIRECT,
	application.WorkspacePreviewTruncated: agentprivatev1.WorkspacePreviewMode_WORKSPACE_PREVIEW_MODE_TRUNCATED,
}

var workspacePreviewDisplays = map[application.WorkspacePreviewDisplay]agentprivatev1.WorkspacePreviewDisplay{
	application.WorkspacePreviewText:  agentprivatev1.WorkspacePreviewDisplay_WORKSPACE_PREVIEW_DISPLAY_TEXT,
	application.WorkspacePreviewPDF:   agentprivatev1.WorkspacePreviewDisplay_WORKSPACE_PREVIEW_DISPLAY_PDF,
	application.WorkspacePreviewImage: agentprivatev1.WorkspacePreviewDisplay_WORKSPACE_PREVIEW_DISPLAY_IMAGE,
}

var workspacePreviewRefusals = map[application.WorkspacePreviewRefusal]agentprivatev1.WorkspacePreviewRefusal{
	application.WorkspacePreviewUnsupported:       agentprivatev1.WorkspacePreviewRefusal_WORKSPACE_PREVIEW_REFUSAL_UNSUPPORTED,
	application.WorkspacePreviewStructureMismatch: agentprivatev1.WorkspacePreviewRefusal_WORKSPACE_PREVIEW_REFUSAL_STRUCTURE_MISMATCH,
	application.WorkspacePreviewEncrypted:         agentprivatev1.WorkspacePreviewRefusal_WORKSPACE_PREVIEW_REFUSAL_ENCRYPTED,
	application.WorkspacePreviewTooLarge:          agentprivatev1.WorkspacePreviewRefusal_WORKSPACE_PREVIEW_REFUSAL_TOO_LARGE,
}

var workspaceEntryKinds = map[application.WorkspaceEntryKind]agentprivatev1.WorkspaceEntryKind{
	application.WorkspaceDirectory: agentprivatev1.WorkspaceEntryKind_WORKSPACE_ENTRY_KIND_DIRECTORY,
	application.WorkspaceFile:      agentprivatev1.WorkspaceEntryKind_WORKSPACE_ENTRY_KIND_FILE,
	application.WorkspaceLink:      agentprivatev1.WorkspaceEntryKind_WORKSPACE_ENTRY_KIND_LINK,
}

// workspaceState names a workspace that cannot be read as a state the reader
// is shown, rather than as an error.
func workspaceState(err error) (agentprivatev1.WorkspaceState, bool) {
	switch {
	case errors.Is(err, application.ErrWorkspaceClosed):
		return agentprivatev1.WorkspaceState_WORKSPACE_STATE_CLOSED, true
	case errors.Is(err, domain.ErrWorkspaceNotCreated):
		return agentprivatev1.WorkspaceState_WORKSPACE_STATE_NOT_CREATED, true
	case errors.Is(err, application.ErrWorkspaceReclaimed):
		return agentprivatev1.WorkspaceState_WORKSPACE_STATE_RECLAIMED, true
	case errors.Is(err, domain.ErrWorkspaceUnavailable):
		return agentprivatev1.WorkspaceState_WORKSPACE_STATE_UNAVAILABLE, true
	}
	return agentprivatev1.WorkspaceState_WORKSPACE_STATE_UNSPECIFIED, false
}

func mapWorkspaceError(err error) error {
	switch {
	case errors.Is(err, application.ErrWorkspacePathNotFound):
		return connect.NewError(connect.CodeNotFound, err.Error()).WithCause(err)
	case errors.Is(err, application.ErrInvalidWorkspacePath):
		return connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	return mapAgentError(err)
}
