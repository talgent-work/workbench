package connectrpc

import (
	"context"
	"strings"

	"google.golang.org/protobuf/types/known/timestamppb"

	agentprivatev1 "github.com/talgent-work/contracts/gen/go/agent/private/v1"
	agentpublicv1 "github.com/talgent-work/contracts/gen/go/agent/public/v1"
	"github.com/talgent-work/contracts/gen/go/agent/public/v1/agentpublicv1connect"
	"github.com/talgent-work/workbench/internal/business/agent/application"
)

// WorkWorkspaceHandler serves a Work's Session Workspace to the
// gateway-asserted person or Application reading it: its owner or a Work
// Share holder, as Work access decides. An Agent, which owns no Work, is
// refused.
type WorkWorkspaceHandler struct {
	application *application.Application
}

var _ agentpublicv1connect.WorkWorkspaceServiceHandler = (*WorkWorkspaceHandler)(nil)

func NewWorkWorkspaceHandler(app *application.Application) *WorkWorkspaceHandler {
	return &WorkWorkspaceHandler{application: app}
}

// readOwnWorkspace reads for the caller itself; no trusted service vouches
// for a Project membership here.
func readOwnWorkspace(ctx context.Context, workID, path string) application.ReadWorkspace {
	return application.ReadWorkspace{ActorID: currentOwner(ctx), WorkID: strings.TrimSpace(workID), Path: path}
}

func (h *WorkWorkspaceHandler) BrowseWorkWorkspace(ctx context.Context, request *agentpublicv1.BrowseWorkWorkspaceRequest) (*agentpublicv1.BrowseWorkWorkspaceResponse, error) {
	listing, err := h.application.BrowseWorkspace(ctx, readOwnWorkspace(ctx, request.GetWorkId(), request.GetPath()))
	if state, unavailable := workspaceState(err); unavailable {
		return &agentpublicv1.BrowseWorkWorkspaceResponse{State: publicWorkspaceStates[state]}, nil
	}
	if err != nil {
		return nil, mapWorkspaceError(err)
	}
	response := &agentpublicv1.BrowseWorkWorkspaceResponse{State: agentpublicv1.WorkWorkspaceState_WORK_WORKSPACE_STATE_AVAILABLE, Total: uint32(listing.Total), Entries: make([]*agentpublicv1.WorkWorkspaceEntry, 0, len(listing.Entries))}
	for _, entry := range listing.Entries {
		response.Entries = append(response.Entries, &agentpublicv1.WorkWorkspaceEntry{
			Name: entry.Name, Kind: publicWorkspaceEntryKinds[entry.Kind], SizeBytes: entry.SizeBytes,
			ModifiedAt: timestamppb.New(entry.ModifiedAt), AttachmentName: entry.AttachmentName,
		})
	}
	return response, nil
}

func (h *WorkWorkspaceHandler) GetWorkWorkspaceFilePreview(ctx context.Context, request *agentpublicv1.GetWorkWorkspaceFilePreviewRequest) (*agentpublicv1.GetWorkWorkspaceFilePreviewResponse, error) {
	preview, err := h.application.PreviewWorkspaceFile(ctx, readOwnWorkspace(ctx, request.GetWorkId(), request.GetPath()))
	if state, unavailable := workspaceState(err); unavailable {
		return &agentpublicv1.GetWorkWorkspaceFilePreviewResponse{State: publicWorkspaceStates[state]}, nil
	}
	if err != nil {
		return nil, mapWorkspaceError(err)
	}
	shown := &agentpublicv1.WorkFilePreview{
		Mode: publicWorkspacePreviewModes[preview.Mode], Display: publicWorkspacePreviewDisplays[preview.Display],
		Refusal: publicWorkspacePreviewRefusals[preview.Refusal], LimitBytes: preview.LimitBytes,
	}
	if preview.Link != nil {
		shown.AccessUrl, shown.ExpiresAt = preview.Link.URL, timestamppb.New(preview.Link.ExpiresAt)
	}
	return &agentpublicv1.GetWorkWorkspaceFilePreviewResponse{State: agentpublicv1.WorkWorkspaceState_WORK_WORKSPACE_STATE_AVAILABLE, Preview: shown}, nil
}

func (h *WorkWorkspaceHandler) CreateWorkWorkspaceFileDownload(ctx context.Context, request *agentpublicv1.CreateWorkWorkspaceFileDownloadRequest) (*agentpublicv1.CreateWorkWorkspaceFileDownloadResponse, error) {
	link, err := h.application.DownloadWorkspaceFile(ctx, readOwnWorkspace(ctx, request.GetWorkId(), request.GetPath()))
	if state, unavailable := workspaceState(err); unavailable {
		return &agentpublicv1.CreateWorkWorkspaceFileDownloadResponse{State: publicWorkspaceStates[state]}, nil
	}
	if err != nil {
		return nil, mapWorkspaceError(err)
	}
	return &agentpublicv1.CreateWorkWorkspaceFileDownloadResponse{State: agentpublicv1.WorkWorkspaceState_WORK_WORKSPACE_STATE_AVAILABLE, AccessUrl: link.URL, ExpiresAt: timestamppb.New(link.ExpiresAt)}, nil
}

// The public contract names the private one's states; workspaceState decides
// them once for both.
var publicWorkspaceStates = map[agentprivatev1.WorkspaceState]agentpublicv1.WorkWorkspaceState{
	agentprivatev1.WorkspaceState_WORKSPACE_STATE_NOT_CREATED: agentpublicv1.WorkWorkspaceState_WORK_WORKSPACE_STATE_NOT_CREATED,
	agentprivatev1.WorkspaceState_WORKSPACE_STATE_CLOSED:      agentpublicv1.WorkWorkspaceState_WORK_WORKSPACE_STATE_CLOSED,
	agentprivatev1.WorkspaceState_WORKSPACE_STATE_RECLAIMED:   agentpublicv1.WorkWorkspaceState_WORK_WORKSPACE_STATE_RECLAIMED,
	agentprivatev1.WorkspaceState_WORKSPACE_STATE_UNAVAILABLE: agentpublicv1.WorkWorkspaceState_WORK_WORKSPACE_STATE_UNAVAILABLE,
}

var publicWorkspaceEntryKinds = map[application.WorkspaceEntryKind]agentpublicv1.WorkWorkspaceEntryKind{
	application.WorkspaceDirectory: agentpublicv1.WorkWorkspaceEntryKind_WORK_WORKSPACE_ENTRY_KIND_DIRECTORY,
	application.WorkspaceFile:      agentpublicv1.WorkWorkspaceEntryKind_WORK_WORKSPACE_ENTRY_KIND_FILE,
	application.WorkspaceLink:      agentpublicv1.WorkWorkspaceEntryKind_WORK_WORKSPACE_ENTRY_KIND_LINK,
}

var publicWorkspacePreviewModes = map[application.WorkspacePreviewMode]agentpublicv1.WorkFilePreviewMode{
	application.WorkspacePreviewRefused:   agentpublicv1.WorkFilePreviewMode_WORK_FILE_PREVIEW_MODE_REFUSED,
	application.WorkspacePreviewDirect:    agentpublicv1.WorkFilePreviewMode_WORK_FILE_PREVIEW_MODE_DIRECT,
	application.WorkspacePreviewTruncated: agentpublicv1.WorkFilePreviewMode_WORK_FILE_PREVIEW_MODE_TRUNCATED,
}

var publicWorkspacePreviewDisplays = map[application.WorkspacePreviewDisplay]agentpublicv1.WorkFilePreviewDisplay{
	application.WorkspacePreviewText:  agentpublicv1.WorkFilePreviewDisplay_WORK_FILE_PREVIEW_DISPLAY_TEXT,
	application.WorkspacePreviewPDF:   agentpublicv1.WorkFilePreviewDisplay_WORK_FILE_PREVIEW_DISPLAY_PDF,
	application.WorkspacePreviewImage: agentpublicv1.WorkFilePreviewDisplay_WORK_FILE_PREVIEW_DISPLAY_IMAGE,
}

var publicWorkspacePreviewRefusals = map[application.WorkspacePreviewRefusal]agentpublicv1.WorkFilePreviewRefusal{
	application.WorkspacePreviewUnsupported:       agentpublicv1.WorkFilePreviewRefusal_WORK_FILE_PREVIEW_REFUSAL_UNSUPPORTED,
	application.WorkspacePreviewStructureMismatch: agentpublicv1.WorkFilePreviewRefusal_WORK_FILE_PREVIEW_REFUSAL_STRUCTURE_MISMATCH,
	application.WorkspacePreviewEncrypted:         agentpublicv1.WorkFilePreviewRefusal_WORK_FILE_PREVIEW_REFUSAL_ENCRYPTED,
	application.WorkspacePreviewTooLarge:          agentpublicv1.WorkFilePreviewRefusal_WORK_FILE_PREVIEW_REFUSAL_TOO_LARGE,
}
