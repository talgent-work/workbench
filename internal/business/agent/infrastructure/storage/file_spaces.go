package storage

import (
	"context"

	"connectrpc.com/connect/v2"
	"github.com/samber/oops"

	storageprivatev1 "github.com/talgent-work/contracts/gen/go/storage/private/v1"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/pkg/storageerror"
)

// OpenDirectory opens a located Directory for its Space Token.
func (c *Client) OpenDirectory(ctx context.Context, directory domain.LocatedDirectory) (string, error) {
	located, err := locatedToProto(directory)
	if err != nil {
		return "", err
	}
	response, err := c.files.OpenDirectory(authorized(ctx, c.token), &storageprivatev1.OpenDirectoryRequest{Directory: located})
	if err != nil {
		return "", oops.With("operation", "storage.open_directory").Wrap(err)
	}
	return response.GetSpaceToken(), nil
}

func (c *Client) ListFiles(ctx context.Context, directory domain.Directory) ([]command.SpaceFile, error) {
	response, err := c.files.ListFiles(authorized(ctx, c.token), &storageprivatev1.ListFilesRequest{Directory: directoryToProto(directory)})
	if err != nil {
		return nil, fileError("storage.list_files", err)
	}
	return filesFromProto(response.GetFiles()), nil
}

func (c *Client) ListDirectoryFiles(ctx context.Context, directories []domain.Directory) ([]command.SpaceFile, error) {
	if len(directories) == 0 {
		return nil, nil
	}
	request := &storageprivatev1.ListDirectoryFilesRequest{}
	for _, directory := range directories {
		request.Directories = append(request.Directories, directoryToProto(directory))
	}
	response, err := c.files.ListDirectoryFiles(authorized(ctx, c.token), request)
	if err != nil {
		return nil, fileError("storage.list_directory_files", err)
	}
	return filesFromProto(response.GetFiles()), nil
}

func (c *Client) IssueUpload(ctx context.Context, position command.FilePosition, spaceToken string, replace bool, contentType string, uploader command.FileActor) (command.FileLink, error) {
	response, err := c.files.IssueUpload(authorized(ctx, c.token), &storageprivatev1.IssueUploadRequest{
		Position: positionToProto(position.Directory, position.Name), SpaceToken: spaceToken, Replace: replace,
		UploaderUserId: uploader.UserID, UploaderApplicationId: uploader.ApplicationID, ContentType: contentType,
	})
	if err != nil {
		return command.FileLink{}, fileError("storage.upload", err)
	}
	return command.FileLink{URL: response.GetUploadUrl(), ExpiresAt: response.GetExpiresAt().AsTime()}, nil
}

func (c *Client) RenameFile(ctx context.Context, position command.FilePosition, spaceToken, newName string, actor command.FileActor) error {
	_, err := c.files.RenameFile(authorized(ctx, c.token), &storageprivatev1.RenameFileRequest{
		Position: positionToProto(position.Directory, position.Name), SpaceToken: spaceToken, NewName: newName,
		ActorUserId: actor.UserID, ActorApplicationId: actor.ApplicationID,
	})
	return fileError("storage.rename", err)
}

func (c *Client) DeleteFile(ctx context.Context, position command.FilePosition, spaceToken string, actor command.FileActor) error {
	_, err := c.files.DeleteFile(authorized(ctx, c.token), &storageprivatev1.DeleteFileRequest{
		Position: positionToProto(position.Directory, position.Name), SpaceToken: spaceToken,
		ActorUserId: actor.UserID, ActorApplicationId: actor.ApplicationID,
	})
	return fileError("storage.delete", err)
}

func (c *Client) IssueAccessLink(ctx context.Context, position command.FilePosition, inline bool) (command.FileLink, error) {
	disposition := storageprivatev1.AccessDisposition_ACCESS_DISPOSITION_DOWNLOAD
	if inline {
		disposition = storageprivatev1.AccessDisposition_ACCESS_DISPOSITION_PREVIEW
	}
	response, err := c.files.IssueAccessLink(authorized(ctx, c.token), &storageprivatev1.IssueAccessLinkRequest{Position: positionToProto(position.Directory, position.Name), Disposition: disposition})
	if err != nil {
		return command.FileLink{}, fileError("storage.access", err)
	}
	return command.FileLink{URL: response.GetAccessUrl(), Path: response.GetAccessPath(), ExpiresAt: response.GetExpiresAt().AsTime()}, nil
}

func (c *Client) CreateShareLink(ctx context.Context, position command.FilePosition, actor command.FileActor) (command.ShareLink, error) {
	response, err := c.files.CreateShareLink(authorized(ctx, c.token), &storageprivatev1.CreateShareLinkRequest{Position: positionToProto(position.Directory, position.Name), CreatedByUserId: actor.UserID, CreatedByApplicationId: actor.ApplicationID})
	if err != nil {
		return command.ShareLink{}, fileError("storage.share", err)
	}
	return shareLinkFromProto(response.GetShareLink()), nil
}

func (c *Client) RevokeShareLinks(ctx context.Context, position command.FilePosition, actor command.FileActor) error {
	_, err := c.files.RevokeShareLinks(authorized(ctx, c.token), &storageprivatev1.RevokeShareLinksRequest{Position: positionToProto(position.Directory, position.Name), CreatedByUserId: actor.UserID, CreatedByApplicationId: actor.ApplicationID})
	return fileError("storage.revoke_shares", err)
}

func (c *Client) FilePreview(ctx context.Context, position command.FilePosition, render bool) (command.FilePreview, error) {
	var preview *storageprivatev1.FilePreview
	if render {
		response, err := c.files.RequestFilePreview(authorized(ctx, c.token), &storageprivatev1.RequestFilePreviewRequest{Position: positionToProto(position.Directory, position.Name)})
		if err != nil {
			return command.FilePreview{}, fileError("storage.request_preview", err)
		}
		preview = response.GetPreview()
	} else {
		response, err := c.files.GetFilePreview(authorized(ctx, c.token), &storageprivatev1.GetFilePreviewRequest{Position: positionToProto(position.Directory, position.Name)})
		if err != nil {
			return command.FilePreview{}, fileError("storage.preview", err)
		}
		preview = response.GetPreview()
	}
	return filePreviewFromProto(preview), nil
}

// fileError maps Storage's explicit refusals to Agent's file outcomes.
func fileError(operation string, err error) error {
	if err == nil {
		return nil
	}
	switch connect.CodeOf(err) {
	case connect.CodeNotFound:
		return command.ErrFileNotFound
	case connect.CodeAlreadyExists:
		return command.ErrFileNameTaken
	case connect.CodeInvalidArgument:
		// Only a person's file name is theirs to correct; any other refusal
		// is Agent's own fault.
		if storageerror.ReasonOf(err) == storageprivatev1.ErrorReason_ERROR_REASON_INVALID_NAME {
			return command.ErrInvalidFileName
		}
	}
	return oops.With("operation", operation).Wrap(err)
}

// filesFromProto maps Storage's files; an Artifact names the Work that
// published it, an Attachment its uploader.
func filesFromProto(files []*storageprivatev1.File) []command.SpaceFile {
	out := make([]command.SpaceFile, 0, len(files))
	for _, file := range files {
		kind := command.FileKindAttachment
		if file.GetWorkId() != "" {
			kind = command.FileKindArtifact
		}
		mapped := command.SpaceFile{
			Directory: directoryFromProto(file.GetPosition().GetDirectory()), Kind: kind, Name: file.GetPosition().GetName(), SizeBytes: file.GetSizeBytes(), ContentType: file.GetContentType(),
			UpdatedAt: file.GetUpdatedAt().AsTime(), Uploader: command.FileActor{UserID: file.GetUploaderUserId(), ApplicationID: file.GetUploaderApplicationId()}, WorkID: file.GetWorkId(),
		}
		for _, link := range file.GetShareLinks() {
			mapped.ShareLinks = append(mapped.ShareLinks, shareLinkFromProto(link))
		}
		out = append(out, mapped)
	}
	return out
}

func shareLinkFromProto(link *storageprivatev1.ShareLink) command.ShareLink {
	return command.ShareLink{Token: link.GetToken(), URL: link.GetUrl(), CreatedBy: command.FileActor{UserID: link.GetCreatedByUserId(), ApplicationID: link.GetCreatedByApplicationId()}, CreatedAt: link.GetCreatedAt().AsTime()}
}

var filePreviewModes = map[storageprivatev1.PreviewMode]command.PreviewMode{
	storageprivatev1.PreviewMode_PREVIEW_MODE_REFUSED: command.PreviewRefused, storageprivatev1.PreviewMode_PREVIEW_MODE_PREVIEW_COPY: command.PreviewCopy,
	storageprivatev1.PreviewMode_PREVIEW_MODE_RENDER: command.PreviewRender, storageprivatev1.PreviewMode_PREVIEW_MODE_DIRECT: command.PreviewDirect,
	storageprivatev1.PreviewMode_PREVIEW_MODE_TRUNCATED: command.PreviewTruncated,
}

var filePreviewDisplays = map[storageprivatev1.PreviewDisplay]command.PreviewDisplay{
	storageprivatev1.PreviewDisplay_PREVIEW_DISPLAY_TEXT: command.PreviewText, storageprivatev1.PreviewDisplay_PREVIEW_DISPLAY_PDF: command.PreviewPDF,
	storageprivatev1.PreviewDisplay_PREVIEW_DISPLAY_IMAGE: command.PreviewImage,
}

var filePreviewRefusals = map[storageprivatev1.PreviewRefusal]command.PreviewRefusal{
	storageprivatev1.PreviewRefusal_PREVIEW_REFUSAL_UNSUPPORTED:         command.PreviewUnsupported,
	storageprivatev1.PreviewRefusal_PREVIEW_REFUSAL_STRUCTURE_MISMATCH:  command.PreviewStructureMismatch,
	storageprivatev1.PreviewRefusal_PREVIEW_REFUSAL_ENCRYPTED:           command.PreviewEncrypted,
	storageprivatev1.PreviewRefusal_PREVIEW_REFUSAL_TOO_LARGE:           command.PreviewTooLarge,
	storageprivatev1.PreviewRefusal_PREVIEW_REFUSAL_CORRUPT:             command.PreviewCorrupt,
	storageprivatev1.PreviewRefusal_PREVIEW_REFUSAL_CONVERTER_REFUSED:   command.PreviewConverterRefused,
	storageprivatev1.PreviewRefusal_PREVIEW_REFUSAL_RENDERING_TOO_LARGE: command.PreviewRenderingTooLarge,
}

// filePreviewFromProto maps Storage's preview; a mode this build does not
// know reads as refused and unsupported, so nothing unknown is shown.
func filePreviewFromProto(preview *storageprivatev1.FilePreview) command.FilePreview {
	mode, known := filePreviewModes[preview.GetMode()]
	if !known {
		return command.FilePreview{Mode: command.PreviewRefused, Refusal: command.PreviewUnsupported}
	}
	result := command.FilePreview{
		Mode: mode, Display: filePreviewDisplays[preview.GetDisplay()], Refusal: filePreviewRefusals[preview.GetRefusal()],
		LimitBytes: preview.GetLimitBytes(), Rendering: preview.GetRendering(),
	}
	if preview.GetAccessUrl() != "" {
		result.Link = &command.FileLink{URL: preview.GetAccessUrl(), Path: preview.GetAccessPath(), ExpiresAt: preview.GetExpiresAt().AsTime()}
	}
	return result
}
