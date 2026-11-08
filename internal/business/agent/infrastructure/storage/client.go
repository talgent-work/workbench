// Package storage is Agent's client of Storage's contract.
package storage

import (
	"context"
	"errors"
	"strings"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/samber/oops"

	storageprivatev1 "github.com/talgent-work/contracts/gen/go/storage/private/v1"
	"github.com/talgent-work/contracts/gen/go/storage/private/v1/storageprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/pkg/storageerror"
)

// Config locates Storage and the control token Agent presents to it.
type Config struct {
	Address string `json:"address" yaml:"address" toml:"address"`
	Token   string `json:"token" yaml:"token" toml:"token"`
}

// Client is Agent's one client of Storage's contract. It names each
// Directory by its owner and name; it never sees a location.
type Client struct {
	mounts storageprivatev1connect.SandboxMountServiceClient
	files  storageprivatev1connect.DirectoryServiceClient
	token  string
}

var (
	_ command.StoragePort = (*Client)(nil)
)

func NewClient(httpClient connecthttp.HTTPClient, config Config) (*Client, error) {
	if strings.TrimSpace(config.Address) == "" || strings.TrimSpace(config.Token) == "" {
		return nil, errors.New("agent: Storage address and control token are required")
	}
	transport := connect.NewClient(connecthttp.NewTransport(httpClient, config.Address, connecthttp.WithReadMaxBytes(0)))
	return &Client{
		mounts: storageprivatev1connect.NewSandboxMountServiceClient(transport),
		files:  storageprivatev1connect.NewDirectoryServiceClient(transport),
		token:  config.Token,
	}, nil
}

// authorized is the context of a call under the control token.
func authorized(ctx context.Context, token string) context.Context {
	ctx, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set("Authorization", "Bearer "+token)
	return ctx
}

func (c *Client) AcquireMounts(ctx context.Context, workID string, mounts []domain.DirectoryMount) (domain.SandboxMounts, error) {
	request := &storageprivatev1.AcquireMountsRequest{WorkId: workID}
	for _, mount := range mounts {
		directory, err := locatedToProto(mount.Directory)
		if err != nil {
			return nil, err
		}
		request.Mounts = append(request.Mounts, &storageprivatev1.MountRequest{Directory: directory, ReadOnly: mount.ReadOnly, MountPoints: mount.MountPoints})
	}
	response, err := c.mounts.AcquireMounts(authorized(ctx, c.token), request)
	if err != nil {
		return nil, oops.With("operation", "storage.acquire_mounts", "work_id", workID).Wrap(err)
	}
	if len(response.GetGrants()) != len(mounts) {
		return nil, oops.With("operation", "storage.acquire_mounts", "work_id", workID).Errorf("storage granted %d of %d Directories", len(response.GetGrants()), len(mounts))
	}
	grants := make(domain.SandboxMounts, 0, len(mounts))
	for _, grant := range response.GetGrants() {
		grants = append(grants, grantFromProto(grant))
	}
	return grants, nil
}

func (c *Client) RequestWorkReclamation(ctx context.Context, workID string) error {
	_, err := c.mounts.RequestWorkReclamation(authorized(ctx, c.token), &storageprivatev1.RequestWorkReclamationRequest{WorkId: workID})
	if err != nil {
		return oops.With("operation", "storage.request_reclamation", "work_id", workID).Wrap(err)
	}
	return nil
}

func (c *Client) WorkspaceView(ctx context.Context, workspace domain.WorkspaceLocation, path string) (application.WorkspaceListing, error) {
	response, err := c.mounts.GetWorkspaceView(authorized(ctx, c.token), &storageprivatev1.GetWorkspaceViewRequest{Layout: workspaceLayoutToProto(workspace.SessionWorkspace), Path: path})
	if err != nil {
		return application.WorkspaceListing{}, workspaceError("storage.workspace_view", err)
	}
	listing := application.WorkspaceListing{Total: int(response.GetTotal()), Entries: make([]application.WorkspaceEntry, 0, len(response.GetEntries()))}
	for _, entry := range response.GetEntries() {
		listing.Entries = append(listing.Entries, application.WorkspaceEntry{
			Name: entry.GetName(), Kind: workspaceEntryKinds[entry.GetKind()], SizeBytes: entry.GetSizeBytes(),
			ModifiedAt: entry.GetModifiedAt().AsTime(), AttachmentName: entry.GetPosition().GetName(),
		})
	}
	return listing, nil
}

func (c *Client) PreviewWorkspaceFile(ctx context.Context, workspace domain.WorkspaceLocation, path string) (application.WorkspaceFilePreview, error) {
	response, err := c.mounts.GetWorkspaceFilePreview(authorized(ctx, c.token), &storageprivatev1.GetWorkspaceFilePreviewRequest{Layout: workspaceLayoutToProto(workspace.SessionWorkspace), Path: path})
	if err != nil {
		return application.WorkspaceFilePreview{}, workspaceError("storage.workspace_file_preview", err)
	}
	return workspacePreviewFromProto(response.GetPreview()), nil
}

// JudgeFilePreview asks Storage how a Managed Work's file is shown, judged
// from its name, size and leading bytes as a Session Workspace file is, and
// the most bytes its reader is served.
func (c *Client) JudgeFilePreview(ctx context.Context, name string, size int64, head []byte) (application.WorkspaceFilePreview, int64, error) {
	response, err := c.mounts.JudgeFilePreview(authorized(ctx, c.token), &storageprivatev1.JudgeFilePreviewRequest{Name: name, SizeBytes: size, Head: head})
	if err != nil {
		return application.WorkspaceFilePreview{}, 0, oops.With("operation", "storage.judge_file_preview").Wrap(err)
	}
	preview := workspacePreviewFromProto(response.GetPreview())
	if preview.Mode == application.WorkspacePreviewRefused {
		return preview, 0, nil
	}
	return preview, response.GetReadLimitBytes(), nil
}

// workspacePreviewFromProto maps Storage's verdict on a workspace file; a
// mode this build does not know shows nothing.
func workspacePreviewFromProto(preview *storageprivatev1.FilePreview) application.WorkspaceFilePreview {
	result := application.WorkspaceFilePreview{
		Mode: previewModes[preview.GetMode()], Display: previewDisplays[preview.GetDisplay()],
		Refusal: previewRefusals[preview.GetRefusal()], LimitBytes: preview.GetLimitBytes(),
	}
	if result.Mode == "" || (result.Mode != application.WorkspacePreviewRefused && result.Display == "") {
		return application.WorkspaceFilePreview{Mode: application.WorkspacePreviewRefused, Refusal: application.WorkspacePreviewUnsupported}
	}
	if preview.GetAccessUrl() != "" {
		result.Link = &application.WorkspaceFileLink{URL: preview.GetAccessUrl(), ExpiresAt: preview.GetExpiresAt().AsTime()}
	}
	return result
}

func (c *Client) WorkspaceFileLink(ctx context.Context, workspace domain.WorkspaceLocation, path string) (application.WorkspaceFileLink, error) {
	response, err := c.mounts.IssueWorkspaceFileLink(authorized(ctx, c.token), &storageprivatev1.IssueWorkspaceFileLinkRequest{Layout: workspaceLayoutToProto(workspace.SessionWorkspace), Path: path})
	if err != nil {
		return application.WorkspaceFileLink{}, workspaceError("storage.workspace_file_link", err)
	}
	return application.WorkspaceFileLink{URL: response.GetAccessUrl(), ExpiresAt: response.GetExpiresAt().AsTime()}, nil
}

// workspaceError maps Storage's refusals to Agent's workspace outcomes.
func workspaceError(operation string, err error) error {
	switch storageerror.ReasonOf(err) {
	case storageprivatev1.ErrorReason_ERROR_REASON_DIRECTORY_NOT_OPEN, storageprivatev1.ErrorReason_ERROR_REASON_DIRECTORY_NOT_ALLOCATED:
		return domain.ErrWorkspaceNotCreated
	case storageprivatev1.ErrorReason_ERROR_REASON_DIRECTORY_RECLAIMED:
		return application.ErrWorkspaceReclaimed
	case storageprivatev1.ErrorReason_ERROR_REASON_INVALID_WORKSPACE_PATH:
		return application.ErrInvalidWorkspacePath
	}
	if connect.CodeOf(err) == connect.CodeNotFound {
		return application.ErrWorkspacePathNotFound
	}
	return oops.With("operation", operation).Wrap(err)
}

var workspaceEntryKinds = map[storageprivatev1.WorkspaceEntryKind]application.WorkspaceEntryKind{
	storageprivatev1.WorkspaceEntryKind_WORKSPACE_ENTRY_KIND_DIRECTORY: application.WorkspaceDirectory,
	storageprivatev1.WorkspaceEntryKind_WORKSPACE_ENTRY_KIND_FILE:      application.WorkspaceFile,
	storageprivatev1.WorkspaceEntryKind_WORKSPACE_ENTRY_KIND_LINK:      application.WorkspaceLink,
}

// Workspace files have no Preview Copies, so only these modes apply.
var previewModes = map[storageprivatev1.PreviewMode]application.WorkspacePreviewMode{
	storageprivatev1.PreviewMode_PREVIEW_MODE_REFUSED: application.WorkspacePreviewRefused, storageprivatev1.PreviewMode_PREVIEW_MODE_DIRECT: application.WorkspacePreviewDirect,
	storageprivatev1.PreviewMode_PREVIEW_MODE_TRUNCATED: application.WorkspacePreviewTruncated,
}

var previewDisplays = map[storageprivatev1.PreviewDisplay]application.WorkspacePreviewDisplay{
	storageprivatev1.PreviewDisplay_PREVIEW_DISPLAY_TEXT: application.WorkspacePreviewText, storageprivatev1.PreviewDisplay_PREVIEW_DISPLAY_PDF: application.WorkspacePreviewPDF,
	storageprivatev1.PreviewDisplay_PREVIEW_DISPLAY_IMAGE: application.WorkspacePreviewImage,
}

var previewRefusals = map[storageprivatev1.PreviewRefusal]application.WorkspacePreviewRefusal{
	storageprivatev1.PreviewRefusal_PREVIEW_REFUSAL_UNSUPPORTED:        application.WorkspacePreviewUnsupported,
	storageprivatev1.PreviewRefusal_PREVIEW_REFUSAL_STRUCTURE_MISMATCH: application.WorkspacePreviewStructureMismatch,
	storageprivatev1.PreviewRefusal_PREVIEW_REFUSAL_ENCRYPTED:          application.WorkspacePreviewEncrypted,
	storageprivatev1.PreviewRefusal_PREVIEW_REFUSAL_TOO_LARGE:          application.WorkspacePreviewTooLarge,
}

func grantFromProto(grant *storageprivatev1.MountGrant) domain.MountGrant {
	return domain.MountGrant{ClaimName: grant.GetClaimName(), SubPath: grant.GetSubPath(), ReadOnly: grant.GetReadOnly()}
}
