package connectrpc

import (
	"context"

	"google.golang.org/protobuf/types/known/timestamppb"

	agentpublicv1 "github.com/talgent-work/contracts/gen/agent/public/v1"
	"github.com/talgent-work/contracts/gen/agent/public/v1/agentpublicv1connect"
	"github.com/talgent-work/workbench/internal/business/agent/application/personalwork"
)

// WorkFileHandler serves Work files for the gateway-asserted person.
type WorkFileHandler struct{ works *personalwork.PersonalWorks }

var _ agentpublicv1connect.WorkFileServiceHandler = (*WorkFileHandler)(nil)

func NewWorkFileHandler(works *personalwork.PersonalWorks) *WorkFileHandler {
	return &WorkFileHandler{works: works}
}

func (h *WorkFileHandler) ListWorkFiles(ctx context.Context, request *agentpublicv1.ListWorkFilesRequest) (*agentpublicv1.ListWorkFilesResponse, error) {
	files, err := h.works.ListFiles(ctx, currentPerson(ctx), request.GetWorkId())
	if err != nil {
		return nil, mapPersonalWorkError(ctx, err)
	}
	response := &agentpublicv1.ListWorkFilesResponse{Files: make([]*agentpublicv1.WorkFile, 0, len(files))}
	for _, file := range files {
		item := &agentpublicv1.WorkFile{
			WorkId: request.GetWorkId(), Kind: workFileKindToProto(file.Kind), Name: file.Name, SizeBytes: file.SizeBytes,
			ContentType: file.ContentType, UpdatedAt: timestamppb.New(file.UpdatedAt), UploaderUserId: file.UploaderUserID,
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
	link, err := h.works.IssueAttachmentUpload(ctx, currentPerson(ctx), msg.GetWorkId(), msg.GetName(), msg.GetReplace(), msg.GetContentType())
	if err != nil {
		return nil, mapPersonalWorkError(ctx, err)
	}
	return &agentpublicv1.CreateWorkAttachmentUploadResponse{UploadUrl: link.URL, ExpiresAt: timestamppb.New(link.ExpiresAt)}, nil
}

func (h *WorkFileHandler) RenameWorkAttachment(ctx context.Context, request *agentpublicv1.RenameWorkAttachmentRequest) (*agentpublicv1.RenameWorkAttachmentResponse, error) {
	msg := request
	if err := h.works.RenameAttachment(ctx, currentPerson(ctx), msg.GetWorkId(), msg.GetName(), msg.GetNewName()); err != nil {
		return nil, mapPersonalWorkError(ctx, err)
	}
	return &agentpublicv1.RenameWorkAttachmentResponse{}, nil
}

func (h *WorkFileHandler) DeleteWorkFile(ctx context.Context, request *agentpublicv1.DeleteWorkFileRequest) (*agentpublicv1.DeleteWorkFileResponse, error) {
	file, err := workFile(request.GetWorkId(), request.GetKind(), request.GetName())
	if err == nil {
		err = h.works.DeleteFile(ctx, currentPerson(ctx), file)
	}
	if err != nil {
		return nil, mapPersonalWorkError(ctx, err)
	}
	return &agentpublicv1.DeleteWorkFileResponse{}, nil
}

func (h *WorkFileHandler) CreateWorkFileAccessLink(ctx context.Context, request *agentpublicv1.CreateWorkFileAccessLinkRequest) (*agentpublicv1.CreateWorkFileAccessLinkResponse, error) {
	file, err := workFile(request.GetWorkId(), request.GetKind(), request.GetName())
	if err != nil {
		return nil, mapPersonalWorkError(ctx, err)
	}
	inline := request.GetDisposition() == agentpublicv1.WorkFileAccessDisposition_WORK_FILE_ACCESS_DISPOSITION_PREVIEW
	link, err := h.works.IssueAccessLink(ctx, currentPerson(ctx), file, inline)
	if err != nil {
		return nil, mapPersonalWorkError(ctx, err)
	}
	return &agentpublicv1.CreateWorkFileAccessLinkResponse{AccessUrl: link.URL, ExpiresAt: timestamppb.New(link.ExpiresAt), AccessPath: link.Path}, nil
}

func (h *WorkFileHandler) CreateWorkFileShareLink(ctx context.Context, request *agentpublicv1.CreateWorkFileShareLinkRequest) (*agentpublicv1.CreateWorkFileShareLinkResponse, error) {
	file, err := workFile(request.GetWorkId(), request.GetKind(), request.GetName())
	if err != nil {
		return nil, mapPersonalWorkError(ctx, err)
	}
	link, err := h.works.CreateShareLink(ctx, currentPerson(ctx), file)
	if err != nil {
		return nil, mapPersonalWorkError(ctx, err)
	}
	return &agentpublicv1.CreateWorkFileShareLinkResponse{ShareLink: shareLinkToProto(link)}, nil
}

func (h *WorkFileHandler) StopWorkFileSharing(ctx context.Context, request *agentpublicv1.StopWorkFileSharingRequest) (*agentpublicv1.StopWorkFileSharingResponse, error) {
	file, err := workFile(request.GetWorkId(), request.GetKind(), request.GetName())
	if err == nil {
		err = h.works.StopSharing(ctx, currentPerson(ctx), file)
	}
	if err != nil {
		return nil, mapPersonalWorkError(ctx, err)
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
		return nil, mapPersonalWorkError(ctx, err)
	}
	preview, err := h.works.FilePreview(ctx, currentPerson(ctx), file, render)
	if err != nil {
		return nil, mapPersonalWorkError(ctx, err)
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

func workFile(workID string, kind agentpublicv1.WorkFileKind, name string) (personalwork.WorkFile, error) {
	file := personalwork.WorkFile{WorkID: workID, Name: name}
	switch kind {
	case agentpublicv1.WorkFileKind_WORK_FILE_KIND_ATTACHMENT:
		file.Kind = personalwork.FileKindAttachment
	case agentpublicv1.WorkFileKind_WORK_FILE_KIND_ARTIFACT:
		file.Kind = personalwork.FileKindArtifact
	default:
		return personalwork.WorkFile{}, personalwork.ErrInvalidRequest
	}
	return file, nil
}

func workFileKindToProto(kind personalwork.FileKind) agentpublicv1.WorkFileKind {
	switch kind {
	case personalwork.FileKindAttachment:
		return agentpublicv1.WorkFileKind_WORK_FILE_KIND_ATTACHMENT
	case personalwork.FileKindArtifact:
		return agentpublicv1.WorkFileKind_WORK_FILE_KIND_ARTIFACT
	default:
		return agentpublicv1.WorkFileKind_WORK_FILE_KIND_UNSPECIFIED
	}
}

func shareLinkToProto(link personalwork.ShareLink) *agentpublicv1.WorkFileShareLink {
	return &agentpublicv1.WorkFileShareLink{Token: link.Token, Url: link.URL, CreatedByUserId: link.CreatedByUserID, CreatedAt: timestamppb.New(link.CreatedAt)}
}

var previewModesToProto = map[personalwork.PreviewMode]agentpublicv1.WorkFilePreviewMode{
	personalwork.PreviewRefused: agentpublicv1.WorkFilePreviewMode_WORK_FILE_PREVIEW_MODE_REFUSED, personalwork.PreviewCopy: agentpublicv1.WorkFilePreviewMode_WORK_FILE_PREVIEW_MODE_PREVIEW_COPY,
	personalwork.PreviewRender: agentpublicv1.WorkFilePreviewMode_WORK_FILE_PREVIEW_MODE_RENDER, personalwork.PreviewDirect: agentpublicv1.WorkFilePreviewMode_WORK_FILE_PREVIEW_MODE_DIRECT,
	personalwork.PreviewTruncated: agentpublicv1.WorkFilePreviewMode_WORK_FILE_PREVIEW_MODE_TRUNCATED,
}

var previewDisplaysToProto = map[personalwork.PreviewDisplay]agentpublicv1.WorkFilePreviewDisplay{
	personalwork.PreviewText: agentpublicv1.WorkFilePreviewDisplay_WORK_FILE_PREVIEW_DISPLAY_TEXT, personalwork.PreviewPDF: agentpublicv1.WorkFilePreviewDisplay_WORK_FILE_PREVIEW_DISPLAY_PDF,
	personalwork.PreviewImage: agentpublicv1.WorkFilePreviewDisplay_WORK_FILE_PREVIEW_DISPLAY_IMAGE,
}

var previewRefusalsToProto = map[personalwork.PreviewRefusal]agentpublicv1.WorkFilePreviewRefusal{
	personalwork.PreviewUnsupported:       agentpublicv1.WorkFilePreviewRefusal_WORK_FILE_PREVIEW_REFUSAL_UNSUPPORTED,
	personalwork.PreviewStructureMismatch: agentpublicv1.WorkFilePreviewRefusal_WORK_FILE_PREVIEW_REFUSAL_STRUCTURE_MISMATCH,
	personalwork.PreviewEncrypted:         agentpublicv1.WorkFilePreviewRefusal_WORK_FILE_PREVIEW_REFUSAL_ENCRYPTED,
	personalwork.PreviewTooLarge:          agentpublicv1.WorkFilePreviewRefusal_WORK_FILE_PREVIEW_REFUSAL_TOO_LARGE,
	personalwork.PreviewCorrupt:           agentpublicv1.WorkFilePreviewRefusal_WORK_FILE_PREVIEW_REFUSAL_CORRUPT,
	personalwork.PreviewConverterRefused:  agentpublicv1.WorkFilePreviewRefusal_WORK_FILE_PREVIEW_REFUSAL_CONVERTER_REFUSED,
	personalwork.PreviewRenderingTooLarge: agentpublicv1.WorkFilePreviewRefusal_WORK_FILE_PREVIEW_REFUSAL_RENDERING_TOO_LARGE,
}
