package connectrpc

import (
	"context"
	"slices"
	"strings"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/types/known/timestamppb"

	storageprivatev1 "github.com/talgent-work/contracts/gen/storage/private/v1"
	"github.com/talgent-work/contracts/gen/storage/private/v1/storageprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/storage/application"
	"github.com/talgent-work/workbench/internal/pkg/authn"
)

// FileSpaceHandler serves the File Space Contract to the contexts that name
// File Space owners, each authenticated by its control token, and the Owner
// File Query alone to the contexts that only read a Library.
type FileSpaceHandler struct {
	application *application.Application
	owners      []string
	readers     []string
}

var _ storageprivatev1connect.FileSpaceServiceHandler = (*FileSpaceHandler)(nil)

// NewFileSpaceHandler admits owners' tokens to the whole contract and
// readers' tokens to the Owner File Query only.
func NewFileSpaceHandler(app *application.Application, owners, readers []string) *FileSpaceHandler {
	return &FileSpaceHandler{application: app, owners: owners, readers: readers}
}

func (h *FileSpaceHandler) OpenFileSpace(ctx context.Context, request *storageprivatev1.OpenFileSpaceRequest) (*storageprivatev1.OpenFileSpaceResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	if err := authenticateAny(info.RequestHeader().Get("Authorization"), h.owners); err != nil {
		return nil, err
	}
	opened, err := h.application.OpenFileSpace(ctx, request.GetOwner())
	if err != nil {
		return nil, mapError(err)
	}
	return &storageprivatev1.OpenFileSpaceResponse{SpaceToken: opened.SpaceToken, InputSource: opened.InputSource}, nil
}

func (h *FileSpaceHandler) ListFiles(ctx context.Context, request *storageprivatev1.ListFilesRequest) (*storageprivatev1.ListFilesResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	if err := authenticateAny(info.RequestHeader().Get("Authorization"), h.owners); err != nil {
		return nil, err
	}
	files, err := h.application.ListFiles(ctx, request.GetOwner())
	if err != nil {
		return nil, mapError(err)
	}
	response := &storageprivatev1.ListFilesResponse{Files: make([]*storageprivatev1.File, 0, len(files))}
	for _, file := range files {
		response.Files = append(response.Files, spaceFileToProto(file))
	}
	return response, nil
}

func (h *FileSpaceHandler) ListOwnerFiles(ctx context.Context, request *storageprivatev1.ListOwnerFilesRequest) (*storageprivatev1.ListOwnerFilesResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	if err := authenticateAny(info.RequestHeader().Get("Authorization"), append(slices.Clip(h.owners), h.readers...)); err != nil {
		return nil, err
	}
	files, err := h.application.ListOwnerFiles(ctx, request.GetOwners())
	if err != nil {
		return nil, mapError(err)
	}
	response := &storageprivatev1.ListOwnerFilesResponse{Files: make([]*storageprivatev1.File, 0, len(files))}
	for _, file := range files {
		response.Files = append(response.Files, storedFileToProto(file))
	}
	return response, nil
}

func (h *FileSpaceHandler) ResolveInputSources(ctx context.Context, request *storageprivatev1.ResolveInputSourcesRequest) (*storageprivatev1.ResolveInputSourcesResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	if err := authenticateAny(info.RequestHeader().Get("Authorization"), h.owners); err != nil {
		return nil, err
	}
	owners, err := h.application.ResolveInputSources(request.GetInputSources())
	if err != nil {
		return nil, mapError(err)
	}
	response := &storageprivatev1.ResolveInputSourcesResponse{InputSources: make([]*storageprivatev1.ResolvedInputSource, 0, len(owners))}
	for i, source := range request.GetInputSources() {
		response.InputSources = append(response.InputSources, &storageprivatev1.ResolvedInputSource{InputSource: source, Owner: owners[i]})
	}
	return response, nil
}

func (h *FileSpaceHandler) IssueAttachmentUpload(ctx context.Context, request *storageprivatev1.IssueAttachmentUploadRequest) (*storageprivatev1.IssueAttachmentUploadResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	if err := authenticateAny(info.RequestHeader().Get("Authorization"), h.owners); err != nil {
		return nil, err
	}
	msg := request
	link, err := h.application.IssueAttachmentUpload(ctx, application.IssueAttachmentUpload{
		Owner: msg.GetOwner(), SpaceToken: msg.GetSpaceToken(), Name: msg.GetName(), Replace: msg.GetReplace(),
		UploaderUserID: msg.GetUploaderUserId(), ContentType: msg.GetContentType(),
	})
	if err != nil {
		return nil, mapError(err)
	}
	return &storageprivatev1.IssueAttachmentUploadResponse{UploadUrl: link.URL, ExpiresAt: timestamppb.New(link.ExpiresAt)}, nil
}

func (h *FileSpaceHandler) RenameAttachment(ctx context.Context, request *storageprivatev1.RenameAttachmentRequest) (*storageprivatev1.RenameAttachmentResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	if err := authenticateAny(info.RequestHeader().Get("Authorization"), h.owners); err != nil {
		return nil, err
	}
	msg := request
	file, err := h.application.RenameAttachment(ctx, msg.GetOwner(), msg.GetSpaceToken(), msg.GetName(), msg.GetNewName(), msg.GetActorUserId())
	if err != nil {
		return nil, mapError(err)
	}
	return &storageprivatev1.RenameAttachmentResponse{File: spaceFileToProto(file)}, nil
}

func (h *FileSpaceHandler) DeleteFile(ctx context.Context, request *storageprivatev1.DeleteFileRequest) (*storageprivatev1.DeleteFileResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	if err := authenticateAny(info.RequestHeader().Get("Authorization"), h.owners); err != nil {
		return nil, err
	}
	msg := request
	if err := h.application.DeleteFile(ctx, msg.GetOwner(), msg.GetSpaceToken(), fileKindFromProto(msg.GetKind()), msg.GetName(), msg.GetActorUserId()); err != nil {
		return nil, mapError(err)
	}
	return &storageprivatev1.DeleteFileResponse{}, nil
}

func (h *FileSpaceHandler) IssueAccessLink(ctx context.Context, request *storageprivatev1.IssueAccessLinkRequest) (*storageprivatev1.IssueAccessLinkResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	if err := authenticateAny(info.RequestHeader().Get("Authorization"), h.owners); err != nil {
		return nil, err
	}
	msg := request
	download := msg.GetDisposition() != storageprivatev1.AccessDisposition_ACCESS_DISPOSITION_PREVIEW
	link, err := h.application.IssueAccessLink(ctx, msg.GetOwner(), fileKindFromProto(msg.GetKind()), msg.GetName(), download)
	if err != nil {
		return nil, mapError(err)
	}
	return &storageprivatev1.IssueAccessLinkResponse{AccessUrl: link.URL, AccessPath: link.Path, ExpiresAt: timestamppb.New(link.ExpiresAt)}, nil
}

func (h *FileSpaceHandler) CreateShareLink(ctx context.Context, request *storageprivatev1.CreateShareLinkRequest) (*storageprivatev1.CreateShareLinkResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	if err := authenticateAny(info.RequestHeader().Get("Authorization"), h.owners); err != nil {
		return nil, err
	}
	msg := request
	link, err := h.application.CreateShareLink(ctx, msg.GetOwner(), fileKindFromProto(msg.GetKind()), msg.GetName(), msg.GetCreatedByUserId())
	if err != nil {
		return nil, mapError(err)
	}
	return &storageprivatev1.CreateShareLinkResponse{ShareLink: shareLinkToProto(link)}, nil
}

func (h *FileSpaceHandler) RevokeShareLinks(ctx context.Context, request *storageprivatev1.RevokeShareLinksRequest) (*storageprivatev1.RevokeShareLinksResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	if err := authenticateAny(info.RequestHeader().Get("Authorization"), h.owners); err != nil {
		return nil, err
	}
	msg := request
	revoked, remaining, err := h.application.RevokeShareLinks(ctx, msg.GetOwner(), fileKindFromProto(msg.GetKind()), msg.GetName(), msg.GetCreatedByUserId())
	if err != nil {
		return nil, mapError(err)
	}
	return &storageprivatev1.RevokeShareLinksResponse{Revoked: int32(revoked), Remaining: int32(remaining)}, nil
}

func (h *FileSpaceHandler) GetFilePreview(ctx context.Context, request *storageprivatev1.GetFilePreviewRequest) (*storageprivatev1.GetFilePreviewResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	if err := authenticateAny(info.RequestHeader().Get("Authorization"), h.owners); err != nil {
		return nil, err
	}
	msg := request
	preview, err := h.application.PreviewFile(ctx, msg.GetOwner(), fileKindFromProto(msg.GetKind()), msg.GetName())
	if err != nil {
		return nil, mapError(err)
	}
	return &storageprivatev1.GetFilePreviewResponse{Preview: filePreviewToProto(preview)}, nil
}

func (h *FileSpaceHandler) RequestFilePreview(ctx context.Context, request *storageprivatev1.RequestFilePreviewRequest) (*storageprivatev1.RequestFilePreviewResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	if err := authenticateAny(info.RequestHeader().Get("Authorization"), h.owners); err != nil {
		return nil, err
	}
	msg := request
	preview, err := h.application.RequestFilePreview(ctx, msg.GetOwner(), fileKindFromProto(msg.GetKind()), msg.GetName())
	if err != nil {
		return nil, mapError(err)
	}
	return &storageprivatev1.RequestFilePreviewResponse{Preview: filePreviewToProto(preview)}, nil
}

// SandboxMountHandler serves the Sandbox Mount Contract to Agent only.
type SandboxMountHandler struct {
	application *application.Application
	token       string
}

var _ storageprivatev1connect.SandboxMountServiceHandler = (*SandboxMountHandler)(nil)

func NewSandboxMountHandler(app *application.Application, token string) *SandboxMountHandler {
	return &SandboxMountHandler{application: app, token: token}
}

func (h *SandboxMountHandler) AcquireMounts(ctx context.Context, request *storageprivatev1.AcquireMountsRequest) (*storageprivatev1.AcquireMountsResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	if err := authenticate(info.RequestHeader().Get("Authorization"), h.token); err != nil {
		return nil, err
	}
	msg := request
	mounts, err := h.application.AcquireMounts(ctx, application.MountRequest{
		HomeOwner: msg.GetHomeOwner(), WorkID: msg.GetWorkId(), WorkspaceReference: msg.GetWorkspaceReference(),
		InputSource: msg.GetInputSource(), SpaceToken: msg.GetSpaceToken(), References: referenceMountsFromProto(msg.GetReferenceMounts()),
	})
	if err != nil {
		return nil, mapError(err)
	}
	response := &storageprivatev1.AcquireMountsResponse{Home: mountGrantToProto(mounts.Home)}
	if mounts.Workspace != nil {
		response.Workspace = mountGrantToProto(*mounts.Workspace)
	}
	if mounts.Inputs != nil {
		response.Inputs = mountGrantToProto(*mounts.Inputs)
	}
	for _, reference := range mounts.References {
		response.References = append(response.References, &storageprivatev1.MountedReference{Name: reference.Name, Grant: mountGrantToProto(reference.Grant)})
	}
	return response, nil
}

func (h *SandboxMountHandler) GetWorkspaceView(ctx context.Context, request *storageprivatev1.GetWorkspaceViewRequest) (*storageprivatev1.GetWorkspaceViewResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	if err := authenticate(info.RequestHeader().Get("Authorization"), h.token); err != nil {
		return nil, err
	}
	msg := request
	listing, err := h.application.WorkspaceView(ctx, application.ViewedWorkspace{Reference: msg.GetWorkspaceReference(), InputSource: msg.GetInputSource(), References: referenceMountsFromProto(msg.GetReferenceMounts())}, msg.GetPath())
	if err != nil {
		return nil, mapError(err)
	}
	return workspaceListingToProto(listing), nil
}

func (h *SandboxMountHandler) GetWorkspaceFilePreview(ctx context.Context, request *storageprivatev1.GetWorkspaceFilePreviewRequest) (*storageprivatev1.GetWorkspaceFilePreviewResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	if err := authenticate(info.RequestHeader().Get("Authorization"), h.token); err != nil {
		return nil, err
	}
	preview, err := h.application.PreviewWorkspaceFile(ctx, application.ViewedWorkspace{Reference: request.GetWorkspaceReference(), References: referenceMountsFromProto(request.GetReferenceMounts())}, request.GetPath())
	if err != nil {
		return nil, mapError(err)
	}
	return &storageprivatev1.GetWorkspaceFilePreviewResponse{Preview: filePreviewToProto(preview)}, nil
}

func (h *SandboxMountHandler) JudgeFilePreview(ctx context.Context, request *storageprivatev1.JudgeFilePreviewRequest) (*storageprivatev1.JudgeFilePreviewResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	if err := authenticate(info.RequestHeader().Get("Authorization"), h.token); err != nil {
		return nil, err
	}
	msg := request
	judged, err := h.application.JudgeFilePreview(msg.GetName(), msg.GetSizeBytes(), msg.GetHead())
	if err != nil {
		return nil, mapError(err)
	}
	return &storageprivatev1.JudgeFilePreviewResponse{
		Preview: filePreviewToProto(application.FilePreview{Verdict: judged.Verdict}), ReadLimitBytes: judged.ReadLimit,
	}, nil
}

func (h *SandboxMountHandler) IssueWorkspaceFileLink(ctx context.Context, request *storageprivatev1.IssueWorkspaceFileLinkRequest) (*storageprivatev1.IssueWorkspaceFileLinkResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	if err := authenticate(info.RequestHeader().Get("Authorization"), h.token); err != nil {
		return nil, err
	}
	link, err := h.application.IssueWorkspaceFileLink(ctx, application.ViewedWorkspace{Reference: request.GetWorkspaceReference(), References: referenceMountsFromProto(request.GetReferenceMounts())}, request.GetPath())
	if err != nil {
		return nil, mapError(err)
	}
	return &storageprivatev1.IssueWorkspaceFileLinkResponse{AccessUrl: link.URL, ExpiresAt: timestamppb.New(link.ExpiresAt)}, nil
}

func (h *SandboxMountHandler) RequestWorkspaceReclamation(ctx context.Context, request *storageprivatev1.RequestWorkspaceReclamationRequest) (*storageprivatev1.RequestWorkspaceReclamationResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	if err := authenticate(info.RequestHeader().Get("Authorization"), h.token); err != nil {
		return nil, err
	}
	if err := h.application.RequestWorkspaceReclamation(ctx, request.GetWorkId(), request.GetWorkspaceReference()); err != nil {
		return nil, mapError(err)
	}
	return &storageprivatev1.RequestWorkspaceReclamationResponse{}, nil
}

// ArtifactHandler serves the Storage Agent Command Contract to Work Agents.
// It is reached only through the native API, which verified the Work
// credential the identity below comes from.
type ArtifactHandler struct {
	application *application.Application
}

var _ storageprivatev1connect.ArtifactServiceHandler = (*ArtifactHandler)(nil)

func NewArtifactHandler(app *application.Application) *ArtifactHandler {
	return &ArtifactHandler{application: app}
}

// workAgent is the verified Work Agent as an Artifact source: its Work, its
// workspace when it needs one, and the Space Token it presents, which names
// the File Space its Work was given.
func workAgent(ctx context.Context, header *connect.Header, withWorkspace bool) (application.ArtifactSource, error) {
	process, ok := authn.NativeProcessFromContext(ctx)
	if !ok || process.WorkID == "" || withWorkspace && process.WorkspaceReference == "" {
		return application.ArtifactSource{}, connect.NewError(connect.CodePermissionDenied, "storage: only a Work Agent changes Artifacts, and only one with a workspace publishes from it")
	}
	return application.ArtifactSource{
		SpaceToken: strings.TrimSpace(header.Get(authn.HeaderSpaceToken)),
		WorkID:     process.WorkID, WorkspaceReference: process.WorkspaceReference,
	}, nil
}

// publishingAgent is the verified Work Agent as the source of a publication:
// a file of its workspace, or the content it sent. The Application admits
// sent content only from a Work without a Session Workspace.
func publishingAgent(ctx context.Context, header *connect.Header, path string, content []byte) (application.ArtifactSource, error) {
	source, err := workAgent(ctx, header, content == nil)
	source.Path, source.Content = path, content
	return source, err
}

func (h *ArtifactHandler) CreateArtifact(ctx context.Context, request *storageprivatev1.CreateArtifactRequest) (*storageprivatev1.CreateArtifactResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	source, err := publishingAgent(ctx, info.RequestHeader(), request.GetPath(), request.Content)
	if err != nil {
		return nil, err
	}
	artifact, err := h.application.PublishArtifact(ctx, source, request.GetName(), false)
	if err != nil {
		return nil, mapError(err)
	}
	return &storageprivatev1.CreateArtifactResponse{Outcome: &storageprivatev1.ArtifactOutcome{Artifact: spaceFileToProto(artifact)}}, nil
}

func (h *ArtifactHandler) ReplaceArtifact(ctx context.Context, request *storageprivatev1.ReplaceArtifactRequest) (*storageprivatev1.ReplaceArtifactResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	source, err := publishingAgent(ctx, info.RequestHeader(), request.GetPath(), request.Content)
	if err != nil {
		return nil, err
	}
	artifact, err := h.application.PublishArtifact(ctx, source, request.GetName(), true)
	if err != nil {
		return nil, mapError(err)
	}
	return &storageprivatev1.ReplaceArtifactResponse{Outcome: &storageprivatev1.ArtifactOutcome{Artifact: spaceFileToProto(artifact)}}, nil
}

func (h *ArtifactHandler) DeleteArtifact(ctx context.Context, request *storageprivatev1.DeleteArtifactRequest) (*storageprivatev1.DeleteArtifactResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	// Deleting needs only the Space Token, never the Work's workspace.
	source, err := workAgent(ctx, info.RequestHeader(), false)
	if err != nil {
		return nil, err
	}
	if err := h.application.DeleteArtifact(ctx, source.SpaceToken, request.GetName(), source.WorkID); err != nil {
		return nil, mapError(err)
	}
	return &storageprivatev1.DeleteArtifactResponse{}, nil
}
