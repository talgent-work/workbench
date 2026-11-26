package application

import (
	"context"
	"errors"

	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

// IssueAccessLink issues a short-lived link for one read of an existing
// Attachment or Artifact.
func (a *Application) IssueAccessLink(ctx context.Context, at Position, download bool) (Link, error) {
	_, _, position, err := a.existingFile(ctx, at)
	if err != nil {
		return Link{}, err
	}
	return a.accessLink(AccessGrant{Directory: position.Directory, Path: position.Name, Download: download}), nil
}

func (a *Application) accessLink(grant AccessGrant) Link {
	expiresAt := a.now().UTC().Add(a.linkTTL)
	link := a.links.AccessLink(grant, expiresAt)
	return Link{URL: link.URL, Path: link.Path, ExpiresAt: expiresAt}
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
	_, backend, err := a.readable(ctx, grant.Directory)
	if err != nil {
		return OpenedFile{}, false, err
	}
	if grant.Workspace {
		opened, err := a.readWorkspaceFile(ctx, backend, grant)
		return opened, grant.Download, err
	}
	position, err := domain.NewFilePosition(grant.Directory, grant.Path)
	if err != nil {
		return OpenedFile{}, false, ErrInvalidLink
	}
	opened, err := backend.Open(ctx, position)
	if err != nil {
		return OpenedFile{}, false, err
	}
	if grant.ContentDigest != "" && opened.File.Attributes.ContentDigest != grant.ContentDigest {
		opened.Content.Close()
		return OpenedFile{}, false, ErrContentChanged
	}
	return opened, grant.Download, nil
}

// CreateShareLink shares an existing Attachment or Artifact, also after the
// work its Directory serves has ended. The link names the file's File ID, so
// a rename or deletion meanwhile needs no coordination with it.
func (a *Application) CreateShareLink(ctx context.Context, at Position, createdBy domain.Actor) (ShareLinkView, error) {
	_, backend, position, err := a.existingFile(ctx, at)
	if err != nil {
		return ShareLinkView{}, err
	}
	file, err := a.identifyFile(ctx, backend, position)
	if err != nil {
		return ShareLinkView{}, err
	}
	var created *domain.ShareLink
	_, err = a.changeDirectory(ctx, position.Directory, func(directory *domain.Directory) error {
		created, err = directory.CreateShareLink(file, createdBy, a.now())
		return err
	})
	if err != nil {
		return ShareLinkView{}, err
	}
	return a.shareLinkView(created), nil
}

// identifyFile is the File ID of the file at the position, taken under the
// Directory's lock so that a Replace carries over the one given.
func (a *Application) identifyFile(ctx context.Context, backend DirectoryBackendPort, position domain.FilePosition) (domain.FileID, error) {
	unlock, err := backend.Lock(ctx, position.Directory)
	if err != nil {
		return "", err
	}
	defer unlock()
	return backend.IdentifyFile(ctx, position)
}

// RevokeShareLinks ends the active Share Links of the file at a position,
// every one when createdBy names no one, or only those createdBy made. Like
// creating one, it is no file write, so the owner's context alone decides it,
// also after the work has ended. A position without a file has no links to
// end: those of a deleted file already answer that it is gone.
func (a *Application) RevokeShareLinks(ctx context.Context, at Position, createdBy domain.Actor) (revoked, remaining int, err error) {
	position, err := at.position()
	if err != nil {
		return 0, 0, err
	}
	directory, err := a.directories.Get(ctx, position.Directory)
	if err != nil {
		return 0, 0, err
	}
	backend, err := a.backends.of(directory)
	if err != nil {
		return 0, 0, err
	}
	stored, err := backend.Stat(ctx, position)
	if errors.Is(err, ErrFileNotFound) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}
	_, err = a.changeDirectory(ctx, position.Directory, func(directory *domain.Directory) error {
		revoked, remaining = directory.RevokeShareLinks(stored.FileID, createdBy)
		return nil
	})
	return revoked, remaining, err
}

// ReadShared opens the current content of an active Share Link's file,
// wherever it is in its Directory; a file gone answers ErrFileNotFound.
func (a *Application) ReadShared(ctx context.Context, token string) (OpenedFile, error) {
	directory, err := a.directories.GetByShareToken(ctx, token)
	if err != nil {
		return OpenedFile{}, err
	}
	link := directory.ShareLink(token)
	if link == nil {
		return OpenedFile{}, domain.ErrShareLinkNotFound
	}
	if !link.Active() {
		return OpenedFile{}, domain.ErrShareLinkRevoked
	}
	if err := directory.AdmitRead(); err != nil {
		return OpenedFile{}, err
	}
	backend, err := a.backends.of(directory)
	if err != nil {
		return OpenedFile{}, err
	}
	return backend.Find(ctx, directory.ID, link.File)
}
