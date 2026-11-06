package connectrpc

import (
	"google.golang.org/protobuf/types/known/timestamppb"

	storageprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/storage/private/v1"
	"github.com/talgent-work/workbench/internal/business/storage/application"
	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

func fileKindFromProto(kind storageprivatev1.FileKind) domain.FileKind {
	switch kind {
	case storageprivatev1.FileKind_FILE_KIND_ATTACHMENT:
		return domain.FileKindAttachment
	case storageprivatev1.FileKind_FILE_KIND_ARTIFACT:
		return domain.FileKindArtifact
	default:
		return ""
	}
}

func fileKindToProto(kind domain.FileKind) storageprivatev1.FileKind {
	switch kind {
	case domain.FileKindAttachment:
		return storageprivatev1.FileKind_FILE_KIND_ATTACHMENT
	case domain.FileKindArtifact:
		return storageprivatev1.FileKind_FILE_KIND_ARTIFACT
	default:
		return storageprivatev1.FileKind_FILE_KIND_UNSPECIFIED
	}
}

func spaceFileToProto(file application.SpaceFile) *storageprivatev1.File {
	message := storedFileToProto(file.StoredFile)
	for _, link := range file.ShareLinks {
		message.ShareLinks = append(message.ShareLinks, shareLinkToProto(link))
	}
	return message
}

func storedFileToProto(file application.StoredFile) *storageprivatev1.File {
	return &storageprivatev1.File{
		Owner: file.Position.Owner, Kind: fileKindToProto(file.Position.Kind), Name: file.Position.Name,
		SizeBytes: file.SizeBytes, ContentType: file.ContentType(), UpdatedAt: timestamppb.New(file.ModifiedAt),
		UploaderUserId: file.Attributes.UploaderUserID, WorkId: file.Attributes.WorkID,
	}
}

func referenceMountsFromProto(mounts []*storageprivatev1.ReferenceMount) []domain.ReferenceMount {
	out := make([]domain.ReferenceMount, 0, len(mounts))
	for _, mount := range mounts {
		out = append(out, domain.ReferenceMount{Name: mount.GetName(), Owner: mount.GetOwner()})
	}
	return out
}

func shareLinkToProto(link application.ShareLinkView) *storageprivatev1.ShareLink {
	return &storageprivatev1.ShareLink{Token: link.Token, Url: link.URL, CreatedByUserId: link.CreatedBy, CreatedAt: timestamppb.New(link.CreatedAt)}
}

func mountGrantToProto(grant application.MountGrant) *storageprivatev1.MountGrant {
	return &storageprivatev1.MountGrant{ClaimName: grant.ClaimName, SubPath: grant.SubPath, ReadOnly: grant.ReadOnly}
}

var workspaceEntryKinds = map[application.WorkspaceEntryKind]storageprivatev1.WorkspaceEntryKind{
	application.WorkspaceDirectory: storageprivatev1.WorkspaceEntryKind_WORKSPACE_ENTRY_KIND_DIRECTORY,
	application.WorkspaceFile:      storageprivatev1.WorkspaceEntryKind_WORKSPACE_ENTRY_KIND_FILE,
	application.WorkspaceLink:      storageprivatev1.WorkspaceEntryKind_WORKSPACE_ENTRY_KIND_LINK,
}

func workspaceListingToProto(listing application.WorkspaceListing) *storageprivatev1.GetWorkspaceViewResponse {
	response := &storageprivatev1.GetWorkspaceViewResponse{Total: uint32(listing.Total), Entries: make([]*storageprivatev1.WorkspaceEntry, 0, len(listing.Entries))}
	for _, entry := range listing.Entries {
		message := &storageprivatev1.WorkspaceEntry{Name: entry.Name, Kind: workspaceEntryKinds[entry.Kind], SizeBytes: entry.SizeBytes, ModifiedAt: timestamppb.New(entry.ModifiedAt)}
		if entry.Attachment != nil {
			message.AttachmentName = entry.Attachment.Name
		}
		response.Entries = append(response.Entries, message)
	}
	return response
}

var previewModes = map[domain.PreviewMode]storageprivatev1.PreviewMode{
	domain.PreviewRefused: storageprivatev1.PreviewMode_PREVIEW_MODE_REFUSED, domain.PreviewShowCopy: storageprivatev1.PreviewMode_PREVIEW_MODE_PREVIEW_COPY,
	domain.PreviewRender: storageprivatev1.PreviewMode_PREVIEW_MODE_RENDER, domain.PreviewDirect: storageprivatev1.PreviewMode_PREVIEW_MODE_DIRECT,
	domain.PreviewTruncated: storageprivatev1.PreviewMode_PREVIEW_MODE_TRUNCATED,
}

var previewDisplays = map[domain.PreviewDisplay]storageprivatev1.PreviewDisplay{
	domain.PreviewDisplayText: storageprivatev1.PreviewDisplay_PREVIEW_DISPLAY_TEXT, domain.PreviewDisplayPDF: storageprivatev1.PreviewDisplay_PREVIEW_DISPLAY_PDF,
	domain.PreviewDisplayImage: storageprivatev1.PreviewDisplay_PREVIEW_DISPLAY_IMAGE,
}

var previewRefusals = map[domain.PreviewRefusal]storageprivatev1.PreviewRefusal{
	domain.PreviewRefusalUnsupported:       storageprivatev1.PreviewRefusal_PREVIEW_REFUSAL_UNSUPPORTED,
	domain.PreviewRefusalStructureMismatch: storageprivatev1.PreviewRefusal_PREVIEW_REFUSAL_STRUCTURE_MISMATCH,
	domain.PreviewRefusalEncrypted:         storageprivatev1.PreviewRefusal_PREVIEW_REFUSAL_ENCRYPTED,
	domain.PreviewRefusalTooLarge:          storageprivatev1.PreviewRefusal_PREVIEW_REFUSAL_TOO_LARGE,
	domain.PreviewRefusalCorrupt:           storageprivatev1.PreviewRefusal_PREVIEW_REFUSAL_CORRUPT,
	domain.PreviewRefusalConverterRefused:  storageprivatev1.PreviewRefusal_PREVIEW_REFUSAL_CONVERTER_REFUSED,
	domain.PreviewRefusalRenderingTooLarge: storageprivatev1.PreviewRefusal_PREVIEW_REFUSAL_RENDERING_TOO_LARGE,
}

func filePreviewToProto(preview application.FilePreview) *storageprivatev1.FilePreview {
	verdict := preview.Verdict
	message := &storageprivatev1.FilePreview{
		Mode: previewModes[verdict.Mode], Display: previewDisplays[verdict.Display],
		Refusal: previewRefusals[verdict.Refusal], LimitBytes: verdict.LimitBytes, Rendering: preview.Rendering,
	}
	if preview.Link != nil {
		message.AccessUrl, message.AccessPath, message.ExpiresAt = preview.Link.URL, preview.Link.Path, timestamppb.New(preview.Link.ExpiresAt)
	}
	return message
}
