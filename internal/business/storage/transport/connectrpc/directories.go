package connectrpc

import (
	"context"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/types/known/timestamppb"

	storageprivatev1 "github.com/talgent-work/contracts/gen/go/storage/private/v1"
	"github.com/talgent-work/contracts/gen/go/storage/private/v1/storageprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/storage/application"
	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

// DirectoryHandler serves the Directory contracts to the contexts that
// locate Directories, each authenticated by its control token. Callers of
// File Spaces reach the whole contract; callers that only replace and
// consult whole content reach opening, content replacement and reading, and
// the Directory file query.
type DirectoryHandler struct {
	application *application.Application
	fileSpaces  []string
	content     []string
}

var _ storageprivatev1connect.DirectoryServiceHandler = (*DirectoryHandler)(nil)

// NewDirectoryHandler admits fileSpaces' tokens to the whole contract and
// content's tokens to the whole-content part of it.
func NewDirectoryHandler(app *application.Application, fileSpaces, content []string) *DirectoryHandler {
	return &DirectoryHandler{application: app, fileSpaces: fileSpaces, content: content}
}

// fileSpaceCaller admits a caller of the whole contract.
func (h *DirectoryHandler) fileSpaceCaller(ctx context.Context) error {
	info, _ := connect.CallInfoForServerContext(ctx)
	return authenticateAny(info.RequestHeader().Get("Authorization"), h.fileSpaces)
}

// contentCaller admits a caller of the whole-content part.
func (h *DirectoryHandler) contentCaller(ctx context.Context) error {
	info, _ := connect.CallInfoForServerContext(ctx)
	return authenticateAny(info.RequestHeader().Get("Authorization"), append(append([]string(nil), h.fileSpaces...), h.content...))
}

func (h *DirectoryHandler) OpenDirectory(ctx context.Context, request *storageprivatev1.OpenDirectoryRequest) (*storageprivatev1.OpenDirectoryResponse, error) {
	if err := h.contentCaller(ctx); err != nil {
		return nil, err
	}
	token, err := h.application.OpenDirectory(ctx, locatedFromProto(request.GetDirectory()))
	if err != nil {
		return nil, mapError(err)
	}
	return &storageprivatev1.OpenDirectoryResponse{SpaceToken: token}, nil
}

func (h *DirectoryHandler) ListFiles(ctx context.Context, request *storageprivatev1.ListFilesRequest) (*storageprivatev1.ListFilesResponse, error) {
	if err := h.fileSpaceCaller(ctx); err != nil {
		return nil, err
	}
	files, err := h.application.ListFiles(ctx, namedFromProto(request.GetDirectory()))
	if err != nil {
		return nil, mapError(err)
	}
	response := &storageprivatev1.ListFilesResponse{Files: make([]*storageprivatev1.File, 0, len(files))}
	for _, file := range files {
		response.Files = append(response.Files, directoryFileToProto(file))
	}
	return response, nil
}

func (h *DirectoryHandler) ListDirectoryFiles(ctx context.Context, request *storageprivatev1.ListDirectoryFilesRequest) (*storageprivatev1.ListDirectoryFilesResponse, error) {
	if err := h.contentCaller(ctx); err != nil {
		return nil, err
	}
	named := make([]application.Named, 0, len(request.GetDirectories()))
	for _, directory := range request.GetDirectories() {
		named = append(named, namedFromProto(directory))
	}
	files, err := h.application.ListDirectoryFiles(ctx, named)
	if err != nil {
		return nil, mapError(err)
	}
	response := &storageprivatev1.ListDirectoryFilesResponse{Files: make([]*storageprivatev1.File, 0, len(files))}
	for _, file := range files {
		response.Files = append(response.Files, storedFileToProto(file))
	}
	return response, nil
}

func (h *DirectoryHandler) IssueUpload(ctx context.Context, request *storageprivatev1.IssueUploadRequest) (*storageprivatev1.IssueUploadResponse, error) {
	if err := h.fileSpaceCaller(ctx); err != nil {
		return nil, err
	}
	link, err := h.application.IssueUpload(ctx, application.IssueUpload{
		Position: positionFromProto(request.GetPosition()), SpaceToken: request.GetSpaceToken(), Replace: request.GetReplace(),
		UploaderUserID: request.GetUploaderUserId(), ContentType: request.GetContentType(),
	})
	if err != nil {
		return nil, mapError(err)
	}
	return &storageprivatev1.IssueUploadResponse{UploadUrl: link.URL, ExpiresAt: timestamppb.New(link.ExpiresAt)}, nil
}

func (h *DirectoryHandler) RenameFile(ctx context.Context, request *storageprivatev1.RenameFileRequest) (*storageprivatev1.RenameFileResponse, error) {
	if err := h.fileSpaceCaller(ctx); err != nil {
		return nil, err
	}
	file, err := h.application.RenameFile(ctx, positionFromProto(request.GetPosition()), request.GetSpaceToken(), request.GetNewName(), request.GetActorUserId())
	if err != nil {
		return nil, mapError(err)
	}
	return &storageprivatev1.RenameFileResponse{File: directoryFileToProto(file)}, nil
}

func (h *DirectoryHandler) DeleteFile(ctx context.Context, request *storageprivatev1.DeleteFileRequest) (*storageprivatev1.DeleteFileResponse, error) {
	if err := h.fileSpaceCaller(ctx); err != nil {
		return nil, err
	}
	if err := h.application.DeleteFile(ctx, positionFromProto(request.GetPosition()), request.GetSpaceToken(), request.GetActorUserId()); err != nil {
		return nil, mapError(err)
	}
	return &storageprivatev1.DeleteFileResponse{}, nil
}

func (h *DirectoryHandler) IssueAccessLink(ctx context.Context, request *storageprivatev1.IssueAccessLinkRequest) (*storageprivatev1.IssueAccessLinkResponse, error) {
	if err := h.fileSpaceCaller(ctx); err != nil {
		return nil, err
	}
	download := request.GetDisposition() != storageprivatev1.AccessDisposition_ACCESS_DISPOSITION_PREVIEW
	link, err := h.application.IssueAccessLink(ctx, positionFromProto(request.GetPosition()), download)
	if err != nil {
		return nil, mapError(err)
	}
	return &storageprivatev1.IssueAccessLinkResponse{AccessUrl: link.URL, AccessPath: link.Path, ExpiresAt: timestamppb.New(link.ExpiresAt)}, nil
}

func (h *DirectoryHandler) CreateShareLink(ctx context.Context, request *storageprivatev1.CreateShareLinkRequest) (*storageprivatev1.CreateShareLinkResponse, error) {
	if err := h.fileSpaceCaller(ctx); err != nil {
		return nil, err
	}
	link, err := h.application.CreateShareLink(ctx, positionFromProto(request.GetPosition()), request.GetCreatedByUserId())
	if err != nil {
		return nil, mapError(err)
	}
	return &storageprivatev1.CreateShareLinkResponse{ShareLink: shareLinkToProto(link)}, nil
}

func (h *DirectoryHandler) RevokeShareLinks(ctx context.Context, request *storageprivatev1.RevokeShareLinksRequest) (*storageprivatev1.RevokeShareLinksResponse, error) {
	if err := h.fileSpaceCaller(ctx); err != nil {
		return nil, err
	}
	revoked, remaining, err := h.application.RevokeShareLinks(ctx, positionFromProto(request.GetPosition()), request.GetCreatedByUserId())
	if err != nil {
		return nil, mapError(err)
	}
	return &storageprivatev1.RevokeShareLinksResponse{Revoked: int32(revoked), Remaining: int32(remaining)}, nil
}

func (h *DirectoryHandler) GetFilePreview(ctx context.Context, request *storageprivatev1.GetFilePreviewRequest) (*storageprivatev1.GetFilePreviewResponse, error) {
	if err := h.fileSpaceCaller(ctx); err != nil {
		return nil, err
	}
	preview, err := h.application.PreviewFile(ctx, positionFromProto(request.GetPosition()))
	if err != nil {
		return nil, mapError(err)
	}
	return &storageprivatev1.GetFilePreviewResponse{Preview: filePreviewToProto(preview)}, nil
}

func (h *DirectoryHandler) RequestFilePreview(ctx context.Context, request *storageprivatev1.RequestFilePreviewRequest) (*storageprivatev1.RequestFilePreviewResponse, error) {
	if err := h.fileSpaceCaller(ctx); err != nil {
		return nil, err
	}
	preview, err := h.application.RequestFilePreview(ctx, positionFromProto(request.GetPosition()))
	if err != nil {
		return nil, mapError(err)
	}
	return &storageprivatev1.RequestFilePreviewResponse{Preview: filePreviewToProto(preview)}, nil
}

func (h *DirectoryHandler) ReplaceContent(ctx context.Context, request *storageprivatev1.ReplaceContentRequest) (*storageprivatev1.ReplaceContentResponse, error) {
	if err := h.contentCaller(ctx); err != nil {
		return nil, err
	}
	files := make([]application.ContentFile, 0, len(request.GetFiles()))
	for _, file := range request.GetFiles() {
		files = append(files, application.ContentFile{Path: domain.ContentPath(file.GetPath()), Content: file.GetContent()})
	}
	if err := h.application.ReplaceContent(ctx, namedFromProto(request.GetDirectory()), request.GetSpaceToken(), files); err != nil {
		return nil, mapError(err)
	}
	return &storageprivatev1.ReplaceContentResponse{}, nil
}

func (h *DirectoryHandler) ReadContent(ctx context.Context, request *storageprivatev1.ReadContentRequest) (*storageprivatev1.ReadContentResponse, error) {
	if err := h.contentCaller(ctx); err != nil {
		return nil, err
	}
	files, err := h.application.ReadContent(ctx, namedFromProto(request.GetDirectory()), request.GetPaths())
	if err != nil {
		return nil, mapError(err)
	}
	response := &storageprivatev1.ReadContentResponse{}
	for _, file := range files {
		response.Files = append(response.Files, &storageprivatev1.ContentFile{Path: string(file.Path), Content: file.Content})
	}
	return response, nil
}
