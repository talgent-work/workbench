package storage

import (
	"context"
	"errors"
	"strings"

	"connectrpc.com/connect"
	"github.com/samber/oops"

	storageprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/storage/private/v1"
	"github.com/talgent-work/workbench/gen/go/talgent/storage/private/v1/storageprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/agent/application/personalwork"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/pkg/storageerror"
)

// FileSpaces is Agent's client of Storage's File Space Contract and Owner
// File Query. Agent names a Personal Work's File Space by the Work's identity.
type FileSpaces struct {
	client storageprivatev1connect.FileSpaceServiceClient
	token  string
}

var _ personalwork.FileSpaces = (*FileSpaces)(nil)

func NewFileSpaces(httpClient connect.HTTPClient, config Config) (*FileSpaces, error) {
	if strings.TrimSpace(config.Address) == "" || strings.TrimSpace(config.Token) == "" {
		return nil, errors.New("agent: Storage address and control token are required")
	}
	return &FileSpaces{client: storageprivatev1connect.NewFileSpaceServiceClient(httpClient, config.Address), token: config.Token}, nil
}

func (s *FileSpaces) OpenFileSpace(ctx context.Context, owner string) (domain.WorkInputSource, error) {
	response, err := s.client.OpenFileSpace(ctx, request(&storageprivatev1.OpenFileSpaceRequest{Owner: owner}, s.token))
	if err != nil {
		return domain.WorkInputSource{}, oops.With("operation", "storage.open_file_space").Wrap(err)
	}
	return domain.WorkInputSource{Reference: response.Msg.GetInputSource(), SpaceToken: response.Msg.GetSpaceToken()}, nil
}

func (s *FileSpaces) ResolveOwners(ctx context.Context, inputSources []string) ([]string, error) {
	response, err := s.client.ResolveInputSources(ctx, request(&storageprivatev1.ResolveInputSourcesRequest{InputSources: inputSources}, s.token))
	if err != nil {
		return nil, oops.With("operation", "storage.resolve_input_sources").Wrap(err)
	}
	resolved := response.Msg.GetInputSources()
	if len(resolved) != len(inputSources) {
		return nil, oops.With("operation", "storage.resolve_input_sources").Errorf("storage answered %d of %d input sources", len(resolved), len(inputSources))
	}
	owners := make([]string, len(resolved))
	for i, source := range resolved {
		owners[i] = source.GetOwner()
	}
	return owners, nil
}

func (s *FileSpaces) ListFiles(ctx context.Context, owner string) ([]personalwork.SpaceFile, error) {
	response, err := s.client.ListFiles(ctx, request(&storageprivatev1.ListFilesRequest{Owner: owner}, s.token))
	if err != nil {
		return nil, fileError("storage.list_files", err)
	}
	return filesFromProto(response.Msg.GetFiles()), nil
}

func (s *FileSpaces) ListOwnerFiles(ctx context.Context, owners []string) ([]personalwork.SpaceFile, error) {
	if len(owners) == 0 {
		return nil, nil
	}
	response, err := s.client.ListOwnerFiles(ctx, request(&storageprivatev1.ListOwnerFilesRequest{Owners: owners}, s.token))
	if err != nil {
		return nil, fileError("storage.list_owner_files", err)
	}
	return filesFromProto(response.Msg.GetFiles()), nil
}

func (s *FileSpaces) IssueAttachmentUpload(ctx context.Context, space domain.WorkInputSource, owner, name string, replace bool, contentType, uploader string) (personalwork.FileLink, error) {
	response, err := s.client.IssueAttachmentUpload(ctx, request(&storageprivatev1.IssueAttachmentUploadRequest{
		Owner: owner, SpaceToken: space.SpaceToken, Name: name, Replace: replace, UploaderUserId: uploader, ContentType: contentType,
	}, s.token))
	if err != nil {
		return personalwork.FileLink{}, fileError("storage.upload", err)
	}
	return personalwork.FileLink{URL: response.Msg.GetUploadUrl(), ExpiresAt: response.Msg.GetExpiresAt().AsTime()}, nil
}

func (s *FileSpaces) RenameAttachment(ctx context.Context, space domain.WorkInputSource, owner, name, newName, actor string) error {
	_, err := s.client.RenameAttachment(ctx, request(&storageprivatev1.RenameAttachmentRequest{Owner: owner, SpaceToken: space.SpaceToken, Name: name, NewName: newName, ActorUserId: actor}, s.token))
	return fileError("storage.rename", err)
}

func (s *FileSpaces) DeleteFile(ctx context.Context, space domain.WorkInputSource, owner string, kind personalwork.FileKind, name, actor string) error {
	_, err := s.client.DeleteFile(ctx, request(&storageprivatev1.DeleteFileRequest{Owner: owner, SpaceToken: space.SpaceToken, Kind: fileKindToProto(kind), Name: name, ActorUserId: actor}, s.token))
	return fileError("storage.delete", err)
}

func (s *FileSpaces) IssueAccessLink(ctx context.Context, owner string, kind personalwork.FileKind, name string, inline bool) (personalwork.FileLink, error) {
	disposition := storageprivatev1.AccessDisposition_ACCESS_DISPOSITION_DOWNLOAD
	if inline {
		disposition = storageprivatev1.AccessDisposition_ACCESS_DISPOSITION_PREVIEW
	}
	response, err := s.client.IssueAccessLink(ctx, request(&storageprivatev1.IssueAccessLinkRequest{Owner: owner, Kind: fileKindToProto(kind), Name: name, Disposition: disposition}, s.token))
	if err != nil {
		return personalwork.FileLink{}, fileError("storage.access", err)
	}
	return personalwork.FileLink{URL: response.Msg.GetAccessUrl(), Path: response.Msg.GetAccessPath(), ExpiresAt: response.Msg.GetExpiresAt().AsTime()}, nil
}

func (s *FileSpaces) CreateShareLink(ctx context.Context, owner string, kind personalwork.FileKind, name, actor string) (personalwork.ShareLink, error) {
	response, err := s.client.CreateShareLink(ctx, request(&storageprivatev1.CreateShareLinkRequest{Owner: owner, Kind: fileKindToProto(kind), Name: name, CreatedByUserId: actor}, s.token))
	if err != nil {
		return personalwork.ShareLink{}, fileError("storage.share", err)
	}
	return shareLinkFromProto(response.Msg.GetShareLink()), nil
}

func (s *FileSpaces) RevokeShareLinks(ctx context.Context, owner string, kind personalwork.FileKind, name, actor string) error {
	_, err := s.client.RevokeShareLinks(ctx, request(&storageprivatev1.RevokeShareLinksRequest{Owner: owner, Kind: fileKindToProto(kind), Name: name, CreatedByUserId: actor}, s.token))
	return fileError("storage.revoke_shares", err)
}

func (s *FileSpaces) FilePreview(ctx context.Context, owner string, kind personalwork.FileKind, name string, render bool) (personalwork.FilePreview, error) {
	var preview *storageprivatev1.FilePreview
	if render {
		response, err := s.client.RequestFilePreview(ctx, request(&storageprivatev1.RequestFilePreviewRequest{Owner: owner, Kind: fileKindToProto(kind), Name: name}, s.token))
		if err != nil {
			return personalwork.FilePreview{}, fileError("storage.request_preview", err)
		}
		preview = response.Msg.GetPreview()
	} else {
		response, err := s.client.GetFilePreview(ctx, request(&storageprivatev1.GetFilePreviewRequest{Owner: owner, Kind: fileKindToProto(kind), Name: name}, s.token))
		if err != nil {
			return personalwork.FilePreview{}, fileError("storage.preview", err)
		}
		preview = response.Msg.GetPreview()
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
		return personalwork.ErrFileNotFound
	case connect.CodeAlreadyExists:
		return personalwork.ErrFileNameTaken
	case connect.CodeInvalidArgument:
		// Only a person's file name is theirs to correct; any other refusal
		// is Agent's own fault.
		if storageerror.ReasonOf(err) == storageprivatev1.ErrorReason_ERROR_REASON_INVALID_NAME {
			return personalwork.ErrInvalidFileName
		}
	}
	return oops.With("operation", operation).Wrap(err)
}

func fileKindToProto(kind personalwork.FileKind) storageprivatev1.FileKind {
	switch kind {
	case personalwork.FileKindAttachment:
		return storageprivatev1.FileKind_FILE_KIND_ATTACHMENT
	case personalwork.FileKindArtifact:
		return storageprivatev1.FileKind_FILE_KIND_ARTIFACT
	default:
		return storageprivatev1.FileKind_FILE_KIND_UNSPECIFIED
	}
}

func filesFromProto(files []*storageprivatev1.File) []personalwork.SpaceFile {
	out := make([]personalwork.SpaceFile, 0, len(files))
	for _, file := range files {
		kind := personalwork.FileKindAttachment
		if file.GetKind() == storageprivatev1.FileKind_FILE_KIND_ARTIFACT {
			kind = personalwork.FileKindArtifact
		}
		mapped := personalwork.SpaceFile{
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

func shareLinkFromProto(link *storageprivatev1.ShareLink) personalwork.ShareLink {
	return personalwork.ShareLink{Token: link.GetToken(), URL: link.GetUrl(), CreatedByUserID: link.GetCreatedByUserId(), CreatedAt: link.GetCreatedAt().AsTime()}
}

var filePreviewModes = map[storageprivatev1.PreviewMode]personalwork.PreviewMode{
	storageprivatev1.PreviewMode_PREVIEW_MODE_REFUSED: personalwork.PreviewRefused, storageprivatev1.PreviewMode_PREVIEW_MODE_PREVIEW_COPY: personalwork.PreviewCopy,
	storageprivatev1.PreviewMode_PREVIEW_MODE_RENDER: personalwork.PreviewRender, storageprivatev1.PreviewMode_PREVIEW_MODE_DIRECT: personalwork.PreviewDirect,
	storageprivatev1.PreviewMode_PREVIEW_MODE_TRUNCATED: personalwork.PreviewTruncated,
}

var filePreviewDisplays = map[storageprivatev1.PreviewDisplay]personalwork.PreviewDisplay{
	storageprivatev1.PreviewDisplay_PREVIEW_DISPLAY_TEXT: personalwork.PreviewText, storageprivatev1.PreviewDisplay_PREVIEW_DISPLAY_PDF: personalwork.PreviewPDF,
	storageprivatev1.PreviewDisplay_PREVIEW_DISPLAY_IMAGE: personalwork.PreviewImage,
}

var filePreviewRefusals = map[storageprivatev1.PreviewRefusal]personalwork.PreviewRefusal{
	storageprivatev1.PreviewRefusal_PREVIEW_REFUSAL_UNSUPPORTED:         personalwork.PreviewUnsupported,
	storageprivatev1.PreviewRefusal_PREVIEW_REFUSAL_STRUCTURE_MISMATCH:  personalwork.PreviewStructureMismatch,
	storageprivatev1.PreviewRefusal_PREVIEW_REFUSAL_ENCRYPTED:           personalwork.PreviewEncrypted,
	storageprivatev1.PreviewRefusal_PREVIEW_REFUSAL_TOO_LARGE:           personalwork.PreviewTooLarge,
	storageprivatev1.PreviewRefusal_PREVIEW_REFUSAL_CORRUPT:             personalwork.PreviewCorrupt,
	storageprivatev1.PreviewRefusal_PREVIEW_REFUSAL_CONVERTER_REFUSED:   personalwork.PreviewConverterRefused,
	storageprivatev1.PreviewRefusal_PREVIEW_REFUSAL_RENDERING_TOO_LARGE: personalwork.PreviewRenderingTooLarge,
}

// filePreviewFromProto maps Storage's preview; a mode this build does not
// know reads as refused and unsupported, so nothing unknown is shown.
func filePreviewFromProto(preview *storageprivatev1.FilePreview) personalwork.FilePreview {
	mode, known := filePreviewModes[preview.GetMode()]
	if !known {
		return personalwork.FilePreview{Mode: personalwork.PreviewRefused, Refusal: personalwork.PreviewUnsupported}
	}
	result := personalwork.FilePreview{
		Mode: mode, Display: filePreviewDisplays[preview.GetDisplay()], Refusal: filePreviewRefusals[preview.GetRefusal()],
		LimitBytes: preview.GetLimitBytes(), Rendering: preview.GetRendering(),
	}
	if preview.GetAccessUrl() != "" {
		result.Link = &personalwork.FileLink{URL: preview.GetAccessUrl(), Path: preview.GetAccessPath(), ExpiresAt: preview.GetExpiresAt().AsTime()}
	}
	return result
}
