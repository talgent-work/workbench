package connectrpc

import (
	"google.golang.org/protobuf/types/known/timestamppb"

	storageprivatev1 "github.com/talgent-work/contracts/gen/go/storage/private/v1"
	"github.com/talgent-work/workbench/internal/business/storage/application"
	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

func namedFromProto(directory *storageprivatev1.Directory) application.Named {
	named := application.Named{Name: directory.GetName()}
	for _, fact := range directory.GetOwner().GetFacts() {
		named.Owner = append(named.Owner, domain.OwnerFact{Condition: fact.GetCondition(), Value: fact.GetValue()})
	}
	return named
}

func namedToProto(named application.Named) *storageprivatev1.Directory {
	owner := &storageprivatev1.DirectoryOwner{}
	for _, fact := range named.Owner {
		owner.Facts = append(owner.Facts, &storageprivatev1.OwnerFact{Condition: fact.Condition, Value: fact.Value})
	}
	return &storageprivatev1.Directory{Owner: owner, Name: named.Name}
}

func positionFromProto(position *storageprivatev1.FilePosition) application.Position {
	return application.Position{Directory: namedFromProto(position.GetDirectory()), Name: position.GetName()}
}

func positionToProto(position domain.FilePosition) *storageprivatev1.FilePosition {
	return &storageprivatev1.FilePosition{Directory: namedToProto(application.AssembleNamed(position.Directory)), Name: position.Name}
}

var backends = map[storageprivatev1.Backend]domain.Backend{
	storageprivatev1.Backend_BACKEND_NAS:     domain.BackendNAS,
	storageprivatev1.Backend_BACKEND_JUICEFS: domain.BackendJuiceFS,
}

var lifetimes = map[storageprivatev1.Lifetime]domain.Lifetime{
	storageprivatev1.Lifetime_LIFETIME_WORK:      domain.LifetimeWork,
	storageprivatev1.Lifetime_LIFETIME_PERMANENT: domain.LifetimePermanent,
}

func locatedFromProto(located *storageprivatev1.LocatedDirectory) application.Located {
	return application.Located{
		Directory: namedFromProto(located.GetDirectory()), Backend: backends[located.GetBackend()], Lifetime: lifetimes[located.GetLifetime()],
	}
}

func layoutFromProto(layout *storageprivatev1.WorkspaceLayout) application.Layout {
	mapped := application.Layout{Workspace: namedFromProto(layout.GetWorkspace())}
	for _, mount := range layout.GetMounts() {
		mapped.Mounts = append(mapped.Mounts, application.Mount{Path: mount.GetPath(), Directory: namedFromProto(mount.GetDirectory())})
	}
	return mapped
}

func directoryFileToProto(file application.DirectoryFile) *storageprivatev1.File {
	message := storedFileToProto(file.StoredFile)
	for _, link := range file.ShareLinks {
		message.ShareLinks = append(message.ShareLinks, shareLinkToProto(link))
	}
	return message
}

func storedFileToProto(file application.StoredFile) *storageprivatev1.File {
	return &storageprivatev1.File{
		Position: positionToProto(file.Position), SizeBytes: file.SizeBytes, ContentType: file.ContentType(), UpdatedAt: timestamppb.New(file.ModifiedAt),
		UploaderUserId: file.Attributes.UploaderUserID, UploaderApplicationId: file.Attributes.UploaderApplicationID, WorkId: file.Attributes.WorkID,
	}
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
		if entry.Position != nil {
			message.Position = positionToProto(*entry.Position)
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
