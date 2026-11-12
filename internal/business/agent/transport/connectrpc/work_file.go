package connectrpc

import (
	"context"

	"google.golang.org/protobuf/types/known/timestamppb"

	agentpublicv1 "github.com/talgent-work/contracts/gen/go/agent/public/v1"
	"github.com/talgent-work/contracts/gen/go/agent/public/v1/agentpublicv1connect"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/standalonework"
)

// WorkFileHandler serves Work files for the gateway-asserted person or
// Application, or a Work's Agent reading its owner's Library.
type WorkFileHandler struct {
	works *standalonework.StandaloneWorks
}

var _ agentpublicv1connect.WorkFileServiceHandler = (*WorkFileHandler)(nil)

func NewWorkFileHandler(works *standalonework.StandaloneWorks) *WorkFileHandler {
	return &WorkFileHandler{works: works}
}

func (h *WorkFileHandler) ListWorkFiles(ctx context.Context, request *agentpublicv1.ListWorkFilesRequest) (*agentpublicv1.ListWorkFilesResponse, error) {
	files, err := h.works.ListFiles(ctx, currentCaller(ctx), request.GetWorkId())
	if err != nil {
		return nil, mapStandaloneWorkError(ctx, err)
	}
	response := &agentpublicv1.ListWorkFilesResponse{Files: make([]*agentpublicv1.WorkFile, 0, len(files))}
	for _, file := range files {
		item := &agentpublicv1.WorkFile{
			WorkId: request.GetWorkId(), Kind: workFileKindToProto(file.Kind), Name: file.Name, SizeBytes: file.SizeBytes,
			ContentType: file.ContentType, UpdatedAt: timestamppb.New(file.UpdatedAt),
			UploaderUserId: file.Uploader.UserID, UploaderApplicationId: file.Uploader.ApplicationID,
		}
		for _, link := range file.ShareLinks {
			item.ShareLinks = append(item.ShareLinks, shareLinkToProto(link))
		}
		response.Files = append(response.Files, item)
	}
	return response, nil
}

func (h *WorkFileHandler) CreateWorkAttachmentUpload(ctx context.Context, request *agentpublicv1.CreateWorkAttachmentUploadRequest) (*agentpublicv1.CreateWorkAttachmentUploadResponse, error) {
	msg := request
	link, err := h.works.IssueAttachmentUpload(ctx, currentCaller(ctx), msg.GetWorkId(), msg.GetName(), msg.GetReplace(), msg.GetContentType())
	if err != nil {
		return nil, mapStandaloneWorkError(ctx, err)
	}
	return &agentpublicv1.CreateWorkAttachmentUploadResponse{UploadUrl: link.URL, ExpiresAt: timestamppb.New(link.ExpiresAt)}, nil
}

func (h *WorkFileHandler) RenameWorkAttachment(ctx context.Context, request *agentpublicv1.RenameWorkAttachmentRequest) (*agentpublicv1.RenameWorkAttachmentResponse, error) {
	msg := request
	if err := h.works.RenameAttachment(ctx, currentCaller(ctx), msg.GetWorkId(), msg.GetName(), msg.GetNewName()); err != nil {
		return nil, mapStandaloneWorkError(ctx, err)
	}
	return &agentpublicv1.RenameWorkAttachmentResponse{}, nil
}

func (h *WorkFileHandler) DeleteWorkFile(ctx context.Context, request *agentpublicv1.DeleteWorkFileRequest) (*agentpublicv1.DeleteWorkFileResponse, error) {
	file, err := workFile(request.GetWorkId(), request.GetKind(), request.GetName())
	if err == nil {
		err = h.works.DeleteFile(ctx, currentCaller(ctx), file)
	}
	if err != nil {
		return nil, mapStandaloneWorkError(ctx, err)
	}
	return &agentpublicv1.DeleteWorkFileResponse{}, nil
}

func (h *WorkFileHandler) CreateWorkFileAccessLink(ctx context.Context, request *agentpublicv1.CreateWorkFileAccessLinkRequest) (*agentpublicv1.CreateWorkFileAccessLinkResponse, error) {
	file, err := workFile(request.GetWorkId(), request.GetKind(), request.GetName())
	if err != nil {
		return nil, mapStandaloneWorkError(ctx, err)
	}
	inline := request.GetDisposition() == agentpublicv1.WorkFileAccessDisposition_WORK_FILE_ACCESS_DISPOSITION_PREVIEW
	link, err := h.works.IssueAccessLink(ctx, currentCaller(ctx), file, inline)
	if err != nil {
		return nil, mapStandaloneWorkError(ctx, err)
	}
	return &agentpublicv1.CreateWorkFileAccessLinkResponse{AccessUrl: link.URL, ExpiresAt: timestamppb.New(link.ExpiresAt), AccessPath: link.Path}, nil
}

func (h *WorkFileHandler) CreateWorkFileShareLink(ctx context.Context, request *agentpublicv1.CreateWorkFileShareLinkRequest) (*agentpublicv1.CreateWorkFileShareLinkResponse, error) {
	file, err := workFile(request.GetWorkId(), request.GetKind(), request.GetName())
	if err != nil {
		return nil, mapStandaloneWorkError(ctx, err)
	}
	link, err := h.works.CreateShareLink(ctx, currentCaller(ctx), file)
	if err != nil {
		return nil, mapStandaloneWorkError(ctx, err)
	}
	return &agentpublicv1.CreateWorkFileShareLinkResponse{ShareLink: shareLinkToProto(link)}, nil
}

func (h *WorkFileHandler) StopWorkFileSharing(ctx context.Context, request *agentpublicv1.StopWorkFileSharingRequest) (*agentpublicv1.StopWorkFileSharingResponse, error) {
	file, err := workFile(request.GetWorkId(), request.GetKind(), request.GetName())
	if err == nil {
		err = h.works.StopSharing(ctx, currentCaller(ctx), file)
	}
	if err != nil {
		return nil, mapStandaloneWorkError(ctx, err)
	}
	return &agentpublicv1.StopWorkFileSharingResponse{}, nil
}

func (h *WorkFileHandler) GetWorkFilePreview(ctx context.Context, request *agentpublicv1.GetWorkFilePreviewRequest) (*agentpublicv1.GetWorkFilePreviewResponse, error) {
	preview, err := h.preview(ctx, request.GetWorkId(), request.GetKind(), request.GetName(), false)
	if err != nil {
		return nil, err
	}
	return &agentpublicv1.GetWorkFilePreviewResponse{Preview: preview}, nil
}

func (h *WorkFileHandler) RequestWorkFilePreview(ctx context.Context, request *agentpublicv1.RequestWorkFilePreviewRequest) (*agentpublicv1.RequestWorkFilePreviewResponse, error) {
	preview, err := h.preview(ctx, request.GetWorkId(), request.GetKind(), request.GetName(), true)
	if err != nil {
		return nil, err
	}
	return &agentpublicv1.RequestWorkFilePreviewResponse{Preview: preview}, nil
}

func (h *WorkFileHandler) preview(ctx context.Context, workID string, kind agentpublicv1.WorkFileKind, name string, render bool) (*agentpublicv1.WorkFilePreview, error) {
	file, err := workFile(workID, kind, name)
	if err != nil {
		return nil, mapStandaloneWorkError(ctx, err)
	}
	preview, err := h.works.FilePreview(ctx, currentCaller(ctx), file, render)
	if err != nil {
		return nil, mapStandaloneWorkError(ctx, err)
	}
	result := &agentpublicv1.WorkFilePreview{
		Mode: previewModesToProto[preview.Mode], Display: previewDisplaysToProto[preview.Display], Refusal: previewRefusalsToProto[preview.Refusal],
		LimitBytes: preview.LimitBytes, Rendering: preview.Rendering,
	}
	if preview.Link != nil {
		result.AccessUrl, result.ExpiresAt = preview.Link.URL, timestamppb.New(preview.Link.ExpiresAt)
	}
	return result, nil
}

func workFile(workID string, kind agentpublicv1.WorkFileKind, name string) (standalonework.WorkFile, error) {
	file := standalonework.WorkFile{WorkID: workID, Name: name}
	switch kind {
	case agentpublicv1.WorkFileKind_WORK_FILE_KIND_ATTACHMENT:
		file.Kind = command.FileKindAttachment
	case agentpublicv1.WorkFileKind_WORK_FILE_KIND_ARTIFACT:
		file.Kind = command.FileKindArtifact
	default:
		return standalonework.WorkFile{}, standalonework.ErrInvalidRequest
	}
	return file, nil
}

func workFileKindToProto(kind command.FileKind) agentpublicv1.WorkFileKind {
	switch kind {
	case command.FileKindAttachment:
		return agentpublicv1.WorkFileKind_WORK_FILE_KIND_ATTACHMENT
	case command.FileKindArtifact:
		return agentpublicv1.WorkFileKind_WORK_FILE_KIND_ARTIFACT
	default:
		return agentpublicv1.WorkFileKind_WORK_FILE_KIND_UNSPECIFIED
	}
}

func shareLinkToProto(link command.ShareLink) *agentpublicv1.WorkFileShareLink {
	return &agentpublicv1.WorkFileShareLink{Token: link.Token, Url: link.URL, CreatedByUserId: link.CreatedBy.UserID, CreatedByApplicationId: link.CreatedBy.ApplicationID, CreatedAt: timestamppb.New(link.CreatedAt)}
}

var previewModesToProto = map[command.PreviewMode]agentpublicv1.WorkFilePreviewMode{
	command.PreviewRefused: agentpublicv1.WorkFilePreviewMode_WORK_FILE_PREVIEW_MODE_REFUSED, command.PreviewCopy: agentpublicv1.WorkFilePreviewMode_WORK_FILE_PREVIEW_MODE_PREVIEW_COPY,
	command.PreviewRender: agentpublicv1.WorkFilePreviewMode_WORK_FILE_PREVIEW_MODE_RENDER, command.PreviewDirect: agentpublicv1.WorkFilePreviewMode_WORK_FILE_PREVIEW_MODE_DIRECT,
	command.PreviewTruncated: agentpublicv1.WorkFilePreviewMode_WORK_FILE_PREVIEW_MODE_TRUNCATED,
}

var previewDisplaysToProto = map[command.PreviewDisplay]agentpublicv1.WorkFilePreviewDisplay{
	command.PreviewText: agentpublicv1.WorkFilePreviewDisplay_WORK_FILE_PREVIEW_DISPLAY_TEXT, command.PreviewPDF: agentpublicv1.WorkFilePreviewDisplay_WORK_FILE_PREVIEW_DISPLAY_PDF,
	command.PreviewImage: agentpublicv1.WorkFilePreviewDisplay_WORK_FILE_PREVIEW_DISPLAY_IMAGE,
}

var previewRefusalsToProto = map[command.PreviewRefusal]agentpublicv1.WorkFilePreviewRefusal{
	command.PreviewUnsupported:       agentpublicv1.WorkFilePreviewRefusal_WORK_FILE_PREVIEW_REFUSAL_UNSUPPORTED,
	command.PreviewStructureMismatch: agentpublicv1.WorkFilePreviewRefusal_WORK_FILE_PREVIEW_REFUSAL_STRUCTURE_MISMATCH,
	command.PreviewEncrypted:         agentpublicv1.WorkFilePreviewRefusal_WORK_FILE_PREVIEW_REFUSAL_ENCRYPTED,
	command.PreviewTooLarge:          agentpublicv1.WorkFilePreviewRefusal_WORK_FILE_PREVIEW_REFUSAL_TOO_LARGE,
	command.PreviewCorrupt:           agentpublicv1.WorkFilePreviewRefusal_WORK_FILE_PREVIEW_REFUSAL_CORRUPT,
	command.PreviewConverterRefused:  agentpublicv1.WorkFilePreviewRefusal_WORK_FILE_PREVIEW_REFUSAL_CONVERTER_REFUSED,
	command.PreviewRenderingTooLarge: agentpublicv1.WorkFilePreviewRefusal_WORK_FILE_PREVIEW_REFUSAL_RENDERING_TOO_LARGE,
}
