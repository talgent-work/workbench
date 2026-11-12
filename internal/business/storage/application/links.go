package application

import (
	"context"

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
// work its Directory serves has ended.
func (a *Application) CreateShareLink(ctx context.Context, at Position, createdBy domain.Actor) (ShareLinkView, error) {
	_, backend, position, err := a.existingFile(ctx, at)
	if err != nil {
		return ShareLinkView{}, err
	}
	var created *domain.ShareLink
	_, err = a.changeDirectory(ctx, position.Directory, func(directory *domain.Directory) error {
		// Checked on every attempt: a rename or deletion recorded meanwhile
		// conflicts with this change, and its retry must see the file gone.
		if _, err := backend.Stat(ctx, position); err != nil {
			return err
		}
		created, err = directory.CreateShareLink(position, createdBy, a.now())
		return err
	})
	if err != nil {
		return ShareLinkView{}, err
	}
	return a.shareLinkView(created), nil
}

// RevokeShareLinks ends a file's active Share Links, every one when createdBy
// names no one, or only those createdBy made. Like creating one, it is no file write, so
// the owner's context alone decides it, also after the work has ended.
func (a *Application) RevokeShareLinks(ctx context.Context, at Position, createdBy domain.Actor) (revoked, remaining int, err error) {
	position, err := at.position()
	if err != nil {
		return 0, 0, err
	}
	_, err = a.changeDirectory(ctx, position.Directory, func(directory *domain.Directory) error {
		revoked, remaining = directory.RevokeShareLinks(position, createdBy)
		return nil
	})
	return revoked, remaining, err
}

// ReadShared opens the current content at an active Share Link's position;
// a file gone from it answers ErrFileNotFound.
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
	return backend.Open(ctx, link.Target)
}
