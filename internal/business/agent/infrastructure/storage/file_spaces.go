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

// OpenFileSpace opens the File Space Agent names by a Personal Work's
// identity; other Works' spaces Agent knows only by their input sources.
func (c *Client) OpenFileSpace(ctx context.Context, owner string) (domain.WorkInputSource, error) {
	response, err := c.files.OpenFileSpace(authorized(ctx, c.token), &storageprivatev1.OpenFileSpaceRequest{Owner: owner})
	if err != nil {
		return domain.WorkInputSource{}, oops.With("operation", "storage.open_file_space").Wrap(err)
	}
	return domain.WorkInputSource{Reference: response.GetInputSource(), SpaceToken: response.GetSpaceToken()}, nil
}

func (c *Client) ResolveOwners(ctx context.Context, inputSources []string) ([]string, error) {
	response, err := c.files.ResolveInputSources(authorized(ctx, c.token), &storageprivatev1.ResolveInputSourcesRequest{InputSources: inputSources})
	if err != nil {
		return nil, oops.With("operation", "storage.resolve_input_sources").Wrap(err)
	}
	resolved := response.GetInputSources()
	if len(resolved) != len(inputSources) {
		return nil, oops.With("operation", "storage.resolve_input_sources").Errorf("storage answered %d of %d input sources", len(resolved), len(inputSources))
	}
	owners := make([]string, len(resolved))
	for i, source := range resolved {
		owners[i] = source.GetOwner()
	}
	return owners, nil
}

func (c *Client) ListFiles(ctx context.Context, owner string) ([]command.SpaceFile, error) {
	response, err := c.files.ListFiles(authorized(ctx, c.token), &storageprivatev1.ListFilesRequest{Owner: owner})
	if err != nil {
		return nil, fileError("storage.list_files", err)
	}
	return filesFromProto(response.GetFiles()), nil
}

func (c *Client) ListOwnerFiles(ctx context.Context, owners []string) ([]command.SpaceFile, error) {
	if len(owners) == 0 {
		return nil, nil
	}
	response, err := c.files.ListOwnerFiles(authorized(ctx, c.token), &storageprivatev1.ListOwnerFilesRequest{Owners: owners})
	if err != nil {
		return nil, fileError("storage.list_owner_files", err)
	}
	return filesFromProto(response.GetFiles()), nil
}

func (c *Client) IssueAttachmentUpload(ctx context.Context, space domain.WorkInputSource, owner, name string, replace bool, contentType, uploader string) (command.FileLink, error) {
	response, err := c.files.IssueAttachmentUpload(authorized(ctx, c.token), &storageprivatev1.IssueAttachmentUploadRequest{
		Owner: owner, SpaceToken: space.SpaceToken, Name: name, Replace: replace, UploaderUserId: uploader, ContentType: contentType,
	})
	if err != nil {
		return command.FileLink{}, fileError("storage.upload", err)
	}
	return command.FileLink{URL: response.GetUploadUrl(), ExpiresAt: response.GetExpiresAt().AsTime()}, nil
}

func (c *Client) RenameAttachment(ctx context.Context, space domain.WorkInputSource, owner, name, newName, actor string) error {
	_, err := c.files.RenameAttachment(authorized(ctx, c.token), &storageprivatev1.RenameAttachmentRequest{Owner: owner, SpaceToken: space.SpaceToken, Name: name, NewName: newName, ActorUserId: actor})
	return fileError("storage.rename", err)
}

func (c *Client) DeleteFile(ctx context.Context, space domain.WorkInputSource, owner string, kind command.FileKind, name, actor string) error {
	_, err := c.files.DeleteFile(authorized(ctx, c.token), &storageprivatev1.DeleteFileRequest{Owner: owner, SpaceToken: space.SpaceToken, Kind: fileKindToProto(kind), Name: name, ActorUserId: actor})
	return fileError("storage.delete", err)
}

func (c *Client) IssueAccessLink(ctx context.Context, owner string, kind command.FileKind, name string, inline bool) (command.FileLink, error) {
	disposition := storageprivatev1.AccessDisposition_ACCESS_DISPOSITION_DOWNLOAD
	if inline {
		disposition = storageprivatev1.AccessDisposition_ACCESS_DISPOSITION_PREVIEW
	}
	response, err := c.files.IssueAccessLink(authorized(ctx, c.token), &storageprivatev1.IssueAccessLinkRequest{Owner: owner, Kind: fileKindToProto(kind), Name: name, Disposition: disposition})
	if err != nil {
		return command.FileLink{}, fileError("storage.access", err)
	}
	return command.FileLink{URL: response.GetAccessUrl(), Path: response.GetAccessPath(), ExpiresAt: response.GetExpiresAt().AsTime()}, nil
}

func (c *Client) CreateShareLink(ctx context.Context, owner string, kind command.FileKind, name, actor string) (command.ShareLink, error) {
	response, err := c.files.CreateShareLink(authorized(ctx, c.token), &storageprivatev1.CreateShareLinkRequest{Owner: owner, Kind: fileKindToProto(kind), Name: name, CreatedByUserId: actor})
	if err != nil {
		return command.ShareLink{}, fileError("storage.share", err)
	}
	return shareLinkFromProto(response.GetShareLink()), nil
}

func (c *Client) RevokeShareLinks(ctx context.Context, owner string, kind command.FileKind, name, actor string) error {
	_, err := c.files.RevokeShareLinks(authorized(ctx, c.token), &storageprivatev1.RevokeShareLinksRequest{Owner: owner, Kind: fileKindToProto(kind), Name: name, CreatedByUserId: actor})
	return fileError("storage.revoke_shares", err)
}

func (c *Client) FilePreview(ctx context.Context, owner string, kind command.FileKind, name string, render bool) (command.FilePreview, error) {
	var preview *storageprivatev1.FilePreview
	if render {
		response, err := c.files.RequestFilePreview(authorized(ctx, c.token), &storageprivatev1.RequestFilePreviewRequest{Owner: owner, Kind: fileKindToProto(kind), Name: name})
		if err != nil {
			return command.FilePreview{}, fileError("storage.request_preview", err)
		}
		preview = response.GetPreview()
	} else {
		response, err := c.files.GetFilePreview(authorized(ctx, c.token), &storageprivatev1.GetFilePreviewRequest{Owner: owner, Kind: fileKindToProto(kind), Name: name})
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

func fileKindToProto(kind command.FileKind) storageprivatev1.FileKind {
	switch kind {
	case command.FileKindAttachment:
		return storageprivatev1.FileKind_FILE_KIND_ATTACHMENT
	case command.FileKindArtifact:
		return storageprivatev1.FileKind_FILE_KIND_ARTIFACT
	default:
		return storageprivatev1.FileKind_FILE_KIND_UNSPECIFIED
	}
}

func filesFromProto(files []*storageprivatev1.File) []command.SpaceFile {
	out := make([]command.SpaceFile, 0, len(files))
	for _, file := range files {
		kind := command.FileKindAttachment
		if file.GetKind() == storageprivatev1.FileKind_FILE_KIND_ARTIFACT {
			kind = command.FileKindArtifact
		}
		mapped := command.SpaceFile{
			Owner: file.GetOwner(), Kind: kind, Name: file.GetName(), SizeBytes: file.GetSizeBytes(), ContentType: file.GetContentType(),
			UpdatedAt: file.GetUpdatedAt().AsTime(), UploaderUserID: file.GetUploaderUserId(), WorkID: file.GetWorkId(),
		}
		for _, link := range file.GetShareLinks() {
			mapped.ShareLinks = append(mapped.ShareLinks, shareLinkFromProto(link))
		}
		out = append(out, mapped)
	}
	return out
}

func shareLinkFromProto(link *storageprivatev1.ShareLink) command.ShareLink {
	return command.ShareLink{Token: link.GetToken(), URL: link.GetUrl(), CreatedByUserID: link.GetCreatedByUserId(), CreatedAt: link.GetCreatedAt().AsTime()}
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
