package application

import (
	"context"
	"errors"
	"io"
	"mime"
	"time"

	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

// OpenedSpace is what opening a File Space returns to its caller.
type OpenedSpace struct {
	SpaceToken  string
	InputSource string
}

// ShareLinkView is one active Share Link of a file.
type ShareLinkView struct {
	Token     string
	URL       string
	CreatedBy string
	CreatedAt time.Time
}

// SpaceFile is a file at its position with its active Share Links.
type SpaceFile struct {
	StoredFile
	ShareLinks []ShareLinkView
}

// Link is an upload link or an Access Link.
type Link struct {
	URL string
	// Path is the link without the public base; only Access Links have it.
	Path      string
	ExpiresAt time.Time
}

// OpenFileSpace opens an owner's space, or returns the open one with its
// token unchanged, and ensures its directories exist.
func (a *Application) OpenFileSpace(ctx context.Context, owner string) (OpenedSpace, error) {
	owner, err := domain.ParseOwner(owner)
	if err != nil {
		return OpenedSpace{}, err
	}
	space, err := a.spaces.Get(ctx, owner)
	if errors.Is(err, domain.ErrSpaceNotOpen) {
		space, err = a.openNewSpace(ctx, owner)
	}
	if err != nil {
		return OpenedSpace{}, err
	}
	if err := a.persistent.EnsureSpace(ctx, space.Owner); err != nil {
		return OpenedSpace{}, err
	}
	return OpenedSpace{SpaceToken: space.SpaceToken, InputSource: space.InputSource()}, nil
}

func (a *Application) openNewSpace(ctx context.Context, owner string) (*domain.FileSpace, error) {
	space, err := domain.OpenFileSpace(owner)
	if err != nil {
		return nil, err
	}
	err = a.spaces.Save(ctx, space)
	if errors.Is(err, domain.ErrConcurrentModification) {
		// A concurrent opening won; its space and token are the space's.
		return a.spaces.Get(ctx, owner)
	}
	return space, err
}

// ListFiles lists an owner's files with their active Share Links. An owner
// whose space never opened has none.
func (a *Application) ListFiles(ctx context.Context, owner string) ([]SpaceFile, error) {
	space, err := a.spaces.Get(ctx, owner)
	if errors.Is(err, domain.ErrSpaceNotOpen) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	stored, err := a.persistent.List(ctx, space.Owner)
	if err != nil {
		return nil, err
	}
	files := make([]SpaceFile, 0, len(stored))
	for _, file := range stored {
		files = append(files, a.spaceFile(space, file))
	}
	return files, nil
}

// IssueAttachmentUpload is one intended Attachment write.
type IssueAttachmentUpload struct {
	Owner, SpaceToken, Name string
	Replace                 bool
	UploaderUserID          string
	ContentType             string
}

// IssueAttachmentUpload admits the write on the Space Token and returns its
// upload link. A taken name refuses a Create and a free one a Replace now;
// the commit decides again when the content arrives.
func (a *Application) IssueAttachmentUpload(ctx context.Context, request IssueAttachmentUpload) (Link, error) {
	writer, err := domain.PersonWriter(request.UploaderUserID)
	if err != nil {
		return Link{}, err
	}
	space, err := a.admittedSpace(ctx, request.Owner, request.SpaceToken, writer)
	if err != nil {
		return Link{}, err
	}
	position, err := domain.NewFilePosition(space.Owner, domain.FileKindAttachment, request.Name)
	if err != nil {
		return Link{}, err
	}
	_, err = a.persistent.Stat(ctx, space.Owner, position)
	switch {
	case err == nil && !request.Replace:
		return Link{}, ErrNameTaken
	case errors.Is(err, ErrFileNotFound) && request.Replace:
		return Link{}, ErrFileNotFound
	case err != nil && !errors.Is(err, ErrFileNotFound):
		return Link{}, err
	}
	expiresAt := a.now().UTC().Add(a.uploadTTL)
	grant := UploadGrant{Owner: space.Owner, Name: position.Name, Replace: request.Replace, UploaderUserID: request.UploaderUserID, ContentType: declaredContentType(request.ContentType)}
	return Link{URL: a.links.UploadURL(grant, expiresAt), ExpiresAt: expiresAt}, nil
}

// UploadAttachment writes the content an upload link admitted. It is staged
// completely before it becomes visible, so no reader sees a partial file.
func (a *Application) UploadAttachment(ctx context.Context, token string, content io.Reader) (SpaceFile, error) {
	grant, err := a.links.VerifyUpload(token, a.now())
	if err != nil {
		return SpaceFile{}, err
	}
	// The person was admitted when the link was issued.
	writer, err := domain.PersonWriter(grant.UploaderUserID)
	if err != nil {
		return SpaceFile{}, err
	}
	position, err := domain.NewFilePosition(grant.Owner, domain.FileKindAttachment, grant.Name)
	if err != nil {
		return SpaceFile{}, err
	}
	stored, err := a.writeFile(ctx, grant.Owner, position, content, FileAttributes{UploaderUserID: grant.UploaderUserID, ContentType: grant.ContentType}, grant.Replace)
	if err != nil {
		return SpaceFile{}, err
	}
	a.fileChanged(ctx, grant.Owner, FileChange{Kind: domain.FileKindAttachment, Name: position.Name, Operation: writtenOperation(grant.Replace), Writer: writer})
	space, err := a.spaces.Get(ctx, grant.Owner)
	if err != nil {
		return SpaceFile{}, err
	}
	return a.spaceFile(space, stored), nil
}

// writeFile writes a file at its position. A create first revokes the links
// an unrecorded deletion left at the free name, so they never reach the new
// file; a replace keeps the position's links.
func (a *Application) writeFile(ctx context.Context, owner string, position domain.FilePosition, content io.Reader, attributes FileAttributes, replace bool) (StoredFile, error) {
	if !replace {
		if err := a.clearStaleShareLinks(ctx, owner, position); err != nil {
			return StoredFile{}, err
		}
	}
	return a.persistent.Write(ctx, owner, position, content, attributes, replace)
}

// clearStaleShareLinks revokes, before a create commits, the links active at
// its position while the name is free. Only the links seen before the name
// was found free are revoked: one created later reaches a file that exists.
func (a *Application) clearStaleShareLinks(ctx context.Context, owner string, position domain.FilePosition) error {
	space, err := a.spaces.Get(ctx, position.Owner)
	if err != nil {
		return err
	}
	stale := space.ShareLinkTokens(position)
	if len(stale) == 0 {
		return nil
	}
	if _, err := a.persistent.Stat(ctx, owner, position); !errors.Is(err, ErrFileNotFound) {
		// Taken: the create is refused and the links stay.
		return err
	}
	_, err = a.changeSpace(ctx, position.Owner, func(space *domain.FileSpace) error {
		space.RevokeStaleShareLinks(position, stale)
		return nil
	})
	return err
}

// RenameAttachment moves an Attachment to a free name for the person the
// owner's context admitted; its active Share Links follow once the rename is carried out.
func (a *Application) RenameAttachment(ctx context.Context, owner, token, name, newName, actorUserID string) (SpaceFile, error) {
	writer, err := domain.PersonWriter(actorUserID)
	if err != nil {
		return SpaceFile{}, err
	}
	space, err := a.admittedSpace(ctx, owner, token, writer)
	if err != nil {
		return SpaceFile{}, err
	}
	from, err := domain.NewFilePosition(space.Owner, domain.FileKindAttachment, name)
	if err != nil {
		return SpaceFile{}, err
	}
	to, err := from.Renamed(newName)
	if err != nil {
		return SpaceFile{}, err
	}
	// A rename only succeeds onto a free name, so the links active there now
	// were left by an unrecorded deletion; links created once the renamed
	// file is there stay.
	stale := space.ShareLinkTokens(to)
	stored, err := a.persistent.Rename(ctx, space.Owner, from, to)
	if err != nil {
		return SpaceFile{}, err
	}
	recorded, err := a.changeSpace(ctx, owner, func(space *domain.FileSpace) error {
		space.RevokeStaleShareLinks(to, stale)
		space.RecordRename(from, to)
		return nil
	})
	if err != nil {
		// Unrecorded, the links would still name the old position, where a
		// later file could appear: move the file back so they keep reaching
		// it. Should that fail too, the next create at the old name clears
		// them before it commits.
		if _, undoErr := a.persistent.Rename(ctx, space.Owner, to, from); undoErr != nil {
			return SpaceFile{}, errors.Join(err, undoErr)
		}
		return SpaceFile{}, err
	}
	a.fileChanged(ctx, space.Owner, FileChange{Kind: domain.FileKindAttachment, Name: to.Name, PreviousName: from.Name, Operation: FileRenamed, Writer: writer})
	return a.spaceFile(recorded, stored), nil
}

// DeleteFile deletes an Attachment or an Artifact for the person the owner's
// context admitted and revokes its Share Links.
func (a *Application) DeleteFile(ctx context.Context, owner, token string, kind domain.FileKind, name, actorUserID string) error {
	writer, err := domain.PersonWriter(actorUserID)
	if err != nil {
		return err
	}
	space, err := a.admittedSpace(ctx, owner, token, writer)
	if err != nil {
		return err
	}
	return a.deleteFile(ctx, space, kind, name, writer)
}

// deleteFile deletes the file and publishes the deletion by writer; deleting
// an absent file changes nothing and publishes nothing.
func (a *Application) deleteFile(ctx context.Context, space *domain.FileSpace, kind domain.FileKind, name string, writer domain.FileWriter) error {
	position, err := domain.NewFilePosition(space.Owner, kind, name)
	if err != nil {
		return err
	}
	removed, err := a.persistent.Remove(ctx, space.Owner, position)
	if err != nil {
		return err
	}
	// The file is gone once removed, whether or not recording its deletion
	// succeeds; a retry removes nothing and so publishes nothing again.
	if removed {
		a.fileChanged(ctx, space.Owner, FileChange{Kind: kind, Name: position.Name, Operation: FileDeleted, Writer: writer})
	}
	_, err = a.changeSpace(ctx, space.Owner, func(space *domain.FileSpace) error {
		space.RecordDeletion(position)
		return nil
	})
	return err
}

// IssueAccessLink issues a short-lived link for one read of an existing file.
func (a *Application) IssueAccessLink(ctx context.Context, owner string, kind domain.FileKind, name string, download bool) (Link, error) {
	space, position, err := a.existingFile(ctx, owner, kind, name)
	if err != nil {
		return Link{}, err
	}
	expiresAt := a.now().UTC().Add(a.linkTTL)
	link := a.links.AccessLink(AccessGrant{Owner: space.Owner, Position: position, Download: download}, expiresAt)
	return Link{URL: link.URL, Path: link.Path, ExpiresAt: expiresAt}, nil
}

// ReadAccess opens the file an Access Link names, with its disposition.
func (a *Application) ReadAccess(ctx context.Context, token string) (OpenedFile, bool, error) {
	grant, err := a.links.VerifyAccess(token, a.now())
	if err != nil {
		return OpenedFile{}, false, err
	}
	if grant.PreviewCopy {
		opened, err := a.readPreviewCopy(ctx, grant)
		return opened, false, err
	}
	if grant.Workspace != "" {
		opened, err := a.readWorkspaceFile(ctx, grant)
		return opened, grant.Download, err
	}
	opened, err := a.persistent.Open(ctx, grant.Owner, grant.Position)
	if err != nil {
		return OpenedFile{}, false, err
	}
	if grant.ContentDigest != "" && opened.File.Attributes.ContentDigest != grant.ContentDigest {
		opened.Content.Close()
		return OpenedFile{}, false, ErrContentChanged
	}
	return opened, grant.Download, nil
}

// CreateShareLink shares an existing file, also after the work the space
// holds has ended.
func (a *Application) CreateShareLink(ctx context.Context, owner string, kind domain.FileKind, name, createdBy string) (ShareLinkView, error) {
	_, position, err := a.existingFile(ctx, owner, kind, name)
	if err != nil {
		return ShareLinkView{}, err
	}
	var created *domain.ShareLink
	_, err = a.changeSpace(ctx, owner, func(space *domain.FileSpace) error {
		// Checked on every attempt: a rename or deletion recorded meanwhile
		// conflicts with this change, and its retry must see the file gone.
		if _, err := a.persistent.Stat(ctx, space.Owner, position); err != nil {
			return err
		}
		created, err = space.CreateShareLink(position, createdBy, a.now())
		return err
	})
	if err != nil {
		return ShareLinkView{}, err
	}
	return a.shareLinkView(created), nil
}

// RevokeShareLinks ends a file's active Share Links, every one or only those
// createdBy made when it is set. Like creating one, it is no file write, so
// the owner's context alone decides it, also after the work has ended.
func (a *Application) RevokeShareLinks(ctx context.Context, owner string, kind domain.FileKind, name, createdBy string) (revoked, remaining int, err error) {
	space, err := a.spaces.Get(ctx, owner)
	if err != nil {
		return 0, 0, err
	}
	position, err := domain.NewFilePosition(space.Owner, kind, name)
	if err != nil {
		return 0, 0, err
	}
	_, err = a.changeSpace(ctx, owner, func(space *domain.FileSpace) error {
		revoked, remaining = space.RevokeShareLinks(position, createdBy)
		return nil
	})
	return revoked, remaining, err
}

// ReadShared opens the current content at an active Share Link's position.
func (a *Application) ReadShared(ctx context.Context, token string) (OpenedFile, error) {
	space, err := a.spaces.GetByShareToken(ctx, token)
	if err != nil {
		return OpenedFile{}, err
	}
	link := space.ShareLink(token)
	if link == nil {
		return OpenedFile{}, domain.ErrShareLinkNotFound
	}
	if !link.Active() {
		return OpenedFile{}, domain.ErrShareLinkRevoked
	}
	return a.persistent.Open(ctx, space.Owner, link.Target)
}

// admittedSpace loads the space and admits a write by writer on its token.
func (a *Application) admittedSpace(ctx context.Context, owner, token string, writer domain.FileWriter) (*domain.FileSpace, error) {
	space, err := a.spaces.Get(ctx, owner)
	if err != nil {
		return nil, err
	}
	if err := space.AdmitFileWrite(token, writer); err != nil {
		return nil, err
	}
	return space, nil
}

func (a *Application) existingFile(ctx context.Context, owner string, kind domain.FileKind, name string) (*domain.FileSpace, domain.FilePosition, error) {
	space, err := a.spaces.Get(ctx, owner)
	if errors.Is(err, domain.ErrSpaceNotOpen) {
		return nil, domain.FilePosition{}, ErrFileNotFound
	}
	if err != nil {
		return nil, domain.FilePosition{}, err
	}
	position, err := domain.NewFilePosition(space.Owner, kind, name)
	if err != nil {
		return nil, domain.FilePosition{}, err
	}
	if _, err := a.persistent.Stat(ctx, space.Owner, position); err != nil {
		return nil, domain.FilePosition{}, err
	}
	return space, position, nil
}

// spaceChangeAttempts bounds retries of a space change racing another.
const spaceChangeAttempts = 5

// changeSpace applies one change to the current space, retrying on a
// concurrent change of the same space so its changes stay serialized.
func (a *Application) changeSpace(ctx context.Context, owner string, change func(*domain.FileSpace) error) (*domain.FileSpace, error) {
	for attempt := 1; ; attempt++ {
		space, err := a.spaces.Get(ctx, owner)
		if err != nil {
			return nil, err
		}
		if err := change(space); err != nil {
			return nil, err
		}
		err = a.spaces.Save(ctx, space)
		if err == nil {
			return space, nil
		}
		if !errors.Is(err, domain.ErrConcurrentModification) || attempt == spaceChangeAttempts {
			return nil, err
		}
	}
}

// declaredContentType keeps a person's declared media type only when it is
// one well-formed type; otherwise the file's name decides it when served.
func declaredContentType(declared string) string {
	mediaType, params, err := mime.ParseMediaType(declared)
	if err != nil {
		return ""
	}
	normalized := mime.FormatMediaType(mediaType, params)
	if normalized == "" || len(normalized) > maxContentTypeBytes {
		return ""
	}
	return normalized
}

// maxContentTypeBytes bounds a declared media type.
const maxContentTypeBytes = 255
