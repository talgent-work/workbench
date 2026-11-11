package application

import (
	"context"
	"errors"
	"io"
	"mime"
	"path"
	"time"

	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

// Position is a File position as a caller names it.
type Position struct {
	Directory Named
	Name      string
}

func (p Position) position() (domain.FilePosition, error) {
	id, err := p.Directory.ID()
	if err != nil {
		return domain.FilePosition{}, err
	}
	return domain.NewFilePosition(id, p.Name)
}

// ShareLinkView is one active Share Link of a file.
type ShareLinkView struct {
	Token     string
	URL       string
	CreatedBy string
	CreatedAt time.Time
}

// DirectoryFile is a file Storage wrote at its position with its active
// Share Links.
type DirectoryFile struct {
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

// ContentType is the declared media type, or the one of the name's
// extension, or a generic binary type.
func (f StoredFile) ContentType() string {
	if f.Attributes.ContentType != "" {
		return f.Attributes.ContentType
	}
	if byName := mime.TypeByExtension(path.Ext(f.Position.Name)); byName != "" {
		return byName
	}
	return "application/octet-stream"
}

// ListFiles lists the files Storage wrote into a Directory with their active
// Share Links. A Directory never opened has none.
func (a *Application) ListFiles(ctx context.Context, named Named) ([]DirectoryFile, error) {
	directory, backend, err := a.readableNamed(ctx, named)
	if errors.Is(err, domain.ErrDirectoryNotOpen) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	stored, err := a.writtenFiles(ctx, backend, directory.ID)
	if err != nil {
		return nil, err
	}
	files := make([]DirectoryFile, 0, len(stored))
	for _, file := range stored {
		files = append(files, a.directoryFile(directory, file))
	}
	return files, nil
}

// DirectoryFiles answer the Directory file query: the files of the named
// Directories, and those named that were reclaimed, each as named.
type DirectoryFiles struct {
	Files     []StoredFile
	Reclaimed []Named
}

// maxDirectoryFileQuery bounds the Directories one Directory file query names.
const maxDirectoryFileQuery = 1000

// ListDirectoryFiles is the Directory file query: the files Storage wrote
// into the given Directories, Directory by Directory as given. The caller
// names only Directories whose files it authorized its reader to see; one
// never opened has none, and one reclaimed answers that it was reclaimed.
func (a *Application) ListDirectoryFiles(ctx context.Context, named []Named) (DirectoryFiles, error) {
	if len(named) > maxDirectoryFileQuery {
		return DirectoryFiles{}, ErrInvalidRequest
	}
	seen := make(map[domain.DirectoryID]bool, len(named))
	var result DirectoryFiles
	for _, each := range named {
		id, err := each.ID()
		if err != nil {
			return DirectoryFiles{}, err
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		_, backend, err := a.readable(ctx, id)
		if errors.Is(err, domain.ErrDirectoryNotOpen) {
			continue
		}
		if errors.Is(err, domain.ErrDirectoryReclaimed) {
			result.Reclaimed = append(result.Reclaimed, each)
			continue
		}
		if err != nil {
			return DirectoryFiles{}, err
		}
		stored, err := a.writtenFiles(ctx, backend, id)
		if err != nil {
			return DirectoryFiles{}, err
		}
		result.Files = append(result.Files, stored...)
	}
	return result, nil
}

// writtenFiles are the files of the Directory that Storage wrote: each
// records its uploader or producing Work.
func (a *Application) writtenFiles(ctx context.Context, backend DirectoryBackendPort, id domain.DirectoryID) ([]StoredFile, error) {
	stored, err := backend.List(ctx, id)
	if err != nil {
		return nil, err
	}
	written := stored[:0]
	for _, file := range stored {
		if file.Attributes.Written() {
			written = append(written, file)
		}
	}
	return written, nil
}

// IssueUpload is one intended Attachment write by a person.
type IssueUpload struct {
	Position       Position
	SpaceToken     string
	Replace        bool
	UploaderUserID string
	ContentType    string
}

// IssueUpload admits the write on the Space Token and returns its upload
// link. A taken name refuses a Create and a free one a Replace now; the
// commit decides again when the content arrives.
func (a *Application) IssueUpload(ctx context.Context, request IssueUpload) (Link, error) {
	writer, err := domain.PersonWriter(request.UploaderUserID)
	if err != nil {
		return Link{}, err
	}
	position, err := request.Position.position()
	if err != nil {
		return Link{}, err
	}
	_, backend, err := a.admittedWrite(ctx, position.Directory, request.SpaceToken, writer)
	if err != nil {
		return Link{}, err
	}
	_, err = backend.Stat(ctx, position)
	switch {
	case err == nil && !request.Replace:
		return Link{}, ErrNameTaken
	case errors.Is(err, ErrFileNotFound) && request.Replace:
		return Link{}, ErrFileNotFound
	case err != nil && !errors.Is(err, ErrFileNotFound):
		return Link{}, err
	}
	expiresAt := a.now().UTC().Add(a.uploadTTL)
	grant := UploadGrant{Position: position, Replace: request.Replace, UploaderUserID: writer.UserID(), ContentType: declaredContentType(request.ContentType)}
	return Link{URL: a.links.UploadURL(grant, expiresAt), ExpiresAt: expiresAt}, nil
}

// Upload writes the content an upload link admitted. It is staged completely
// before it becomes visible, so no reader sees a partial file. The person
// was admitted when the link was issued; the Directory must still be
// Allocated.
func (a *Application) Upload(ctx context.Context, token string, content io.Reader) (DirectoryFile, error) {
	grant, err := a.links.VerifyUpload(token, a.now())
	if err != nil {
		return DirectoryFile{}, err
	}
	writer, err := domain.PersonWriter(grant.UploaderUserID)
	if err != nil {
		return DirectoryFile{}, err
	}
	directory, err := a.directories.Get(ctx, grant.Position.Directory)
	if err != nil {
		return DirectoryFile{}, err
	}
	// The signed link stands for the Space Token presented when it was
	// issued; the write is admitted again on it, so a Directory reclaimed
	// meanwhile refuses it.
	if err := directory.AdmitFileWrite(directory.SpaceToken, writer); err != nil {
		return DirectoryFile{}, err
	}
	backend, err := a.backends.of(directory)
	if err != nil {
		return DirectoryFile{}, err
	}
	stored, err := a.writeFile(ctx, backend, grant.Position, content, FileAttributes{UploaderUserID: writer.UserID(), ContentType: grant.ContentType}, grant.Replace)
	if err != nil {
		return DirectoryFile{}, err
	}
	a.fileChanged(ctx, FileChange{Directory: directory.ID, Name: grant.Position.Name, Operation: writtenOperation(grant.Replace), Writer: writer})
	return a.directoryFile(directory, stored), nil
}

// writeFile stages the content, then commits it at its position under the
// Directory's lock, following its mapped paths. A create first revokes the
// links an unrecorded deletion left at the free name, so they never reach
// the new file; a replace keeps the position's links.
func (a *Application) writeFile(ctx context.Context, backend DirectoryBackendPort, position domain.FilePosition, content io.Reader, attributes FileAttributes, replace bool) (StoredFile, error) {
	staged, err := backend.Stage(ctx, content, attributes)
	if err != nil {
		return StoredFile{}, err
	}
	// A committed content is no longer staged, so this removes only one that
	// was not.
	defer backend.Discard(staged)
	var stored StoredFile
	err = a.mappedChange(ctx, backend, position.Directory, func(directory *domain.Directory) error {
		if !replace {
			if err := a.clearStaleShareLinks(ctx, backend, position); err != nil {
				return err
			}
		}
		var commitErr error
		stored, commitErr = backend.Commit(ctx, staged, position, replace, directory.Mappings)
		return a.carriedOut(ctx, position.Directory, commitErr)
	})
	return stored, err
}

// mappedChange runs a change of the Directory's files holding its lock,
// with the Directory as it stands under the lock, so the change follows
// exactly the mapped paths it has.
func (a *Application) mappedChange(ctx context.Context, backend DirectoryBackendPort, id domain.DirectoryID, change func(*domain.Directory) error) error {
	unlock, err := backend.Lock(ctx, id)
	if err != nil {
		return err
	}
	defer unlock()
	directory, err := a.directories.Get(ctx, id)
	if err != nil {
		return err
	}
	return change(directory)
}

// carriedOut accepts a change carried out in the Directory whose mapped
// paths fell behind: they show fewer files until the Mapped View repair
// aligns them.
func (a *Application) carriedOut(ctx context.Context, id domain.DirectoryID, err error) error {
	if errors.Is(err, ErrMappedViewBehind) {
		a.logger.WarnContext(ctx, "file change carried out; its mapped paths wait for the repair", "directory", id.Name, "error", err)
		return nil
	}
	return err
}

// clearStaleShareLinks revokes, before a create commits, the links active at
// its position while the name is free. Only the links seen before the name
// was found free are revoked: one created later reaches a file that exists.
func (a *Application) clearStaleShareLinks(ctx context.Context, backend DirectoryBackendPort, position domain.FilePosition) error {
	directory, err := a.directories.Get(ctx, position.Directory)
	if err != nil {
		return err
	}
	stale := directory.ShareLinkTokens(position)
	if len(stale) == 0 {
		return nil
	}
	if _, err := backend.Stat(ctx, position); !errors.Is(err, ErrFileNotFound) {
		// Taken: the create is refused and the links stay.
		return err
	}
	_, err = a.changeDirectory(ctx, position.Directory, func(directory *domain.Directory) error {
		directory.RevokeStaleShareLinks(position, stale)
		return nil
	})
	return err
}

// RenameFile moves a file to a free name for the person the owner's context
// admitted; its active Share Links follow once the rename is carried out.
func (a *Application) RenameFile(ctx context.Context, at Position, token, newName, actorUserID string) (DirectoryFile, error) {
	writer, err := domain.PersonWriter(actorUserID)
	if err != nil {
		return DirectoryFile{}, err
	}
	from, err := at.position()
	if err != nil {
		return DirectoryFile{}, err
	}
	to, err := from.Renamed(newName)
	if err != nil {
		return DirectoryFile{}, err
	}
	_, backend, err := a.admittedWrite(ctx, from.Directory, token, writer)
	if err != nil {
		return DirectoryFile{}, err
	}
	var stored StoredFile
	var recorded *domain.Directory
	err = a.mappedChange(ctx, backend, from.Directory, func(directory *domain.Directory) error {
		// A rename only succeeds onto a free name, so the links active there
		// now were left by an unrecorded deletion; links created once the
		// renamed file is there stay.
		stale := directory.ShareLinkTokens(to)
		var renameErr error
		stored, renameErr = backend.Rename(ctx, from, to, directory.Mappings)
		if err := a.carriedOut(ctx, from.Directory, renameErr); err != nil {
			return err
		}
		var recordErr error
		recorded, recordErr = a.changeDirectory(ctx, from.Directory, func(directory *domain.Directory) error {
			directory.RevokeStaleShareLinks(to, stale)
			directory.RecordRename(from, to)
			return nil
		})
		if recordErr != nil {
			// Unrecorded, the links would still name the old position, where
			// a later file could appear: move the file back so they keep
			// reaching it. Should that fail too, the next create at the old
			// name clears them before it commits.
			if _, undoErr := backend.Rename(ctx, to, from, directory.Mappings); a.carriedOut(ctx, from.Directory, undoErr) != nil {
				return errors.Join(recordErr, undoErr)
			}
		}
		return recordErr
	})
	if err != nil {
		return DirectoryFile{}, err
	}
	a.fileChanged(ctx, FileChange{Directory: from.Directory, Name: to.Name, PreviousName: from.Name, Operation: FileRenamed, Writer: writer})
	return a.directoryFile(recorded, stored), nil
}

// DeleteFile deletes a file for the person the owner's context admitted and
// revokes its Share Links; deleting an absent file succeeds.
func (a *Application) DeleteFile(ctx context.Context, at Position, token, actorUserID string) error {
	writer, err := domain.PersonWriter(actorUserID)
	if err != nil {
		return err
	}
	position, err := at.position()
	if err != nil {
		return err
	}
	_, backend, err := a.admittedWrite(ctx, position.Directory, token, writer)
	if err != nil {
		return err
	}
	return a.deleteFile(ctx, backend, position, writer)
}

// deleteFile deletes the file, its links in the mapped paths first, and
// publishes the deletion by writer; deleting an absent file changes nothing
// and publishes nothing.
func (a *Application) deleteFile(ctx context.Context, backend DirectoryBackendPort, position domain.FilePosition, writer domain.FileWriter) error {
	return a.mappedChange(ctx, backend, position.Directory, func(directory *domain.Directory) error {
		return a.removeFile(ctx, backend, directory, position, writer)
	})
}

func (a *Application) removeFile(ctx context.Context, backend DirectoryBackendPort, directory *domain.Directory, position domain.FilePosition, writer domain.FileWriter) error {
	removed, err := backend.Remove(ctx, position, directory.Mappings)
	if err != nil {
		return err
	}
	// The file is gone once removed, whether or not recording its deletion
	// succeeds; a retry removes nothing and so publishes nothing again.
	if removed {
		a.fileChanged(ctx, FileChange{Directory: position.Directory, Name: position.Name, Operation: FileDeleted, Writer: writer})
	}
	_, err = a.changeDirectory(ctx, position.Directory, func(directory *domain.Directory) error {
		directory.RecordDeletion(position)
		return nil
	})
	return err
}

// admittedWrite loads the Directory and admits a write by writer on its
// token.
func (a *Application) admittedWrite(ctx context.Context, id domain.DirectoryID, token string, writer domain.FileWriter) (*domain.Directory, DirectoryBackendPort, error) {
	directory, err := a.directories.Get(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if err := directory.AdmitFileWrite(token, writer); err != nil {
		return nil, nil, err
	}
	backend, err := a.backends.of(directory)
	return directory, backend, err
}

// existingFile finds an Attachment or Artifact at a position a reader names.
func (a *Application) existingFile(ctx context.Context, at Position) (*domain.Directory, DirectoryBackendPort, domain.FilePosition, error) {
	position, err := at.position()
	if err != nil {
		return nil, nil, domain.FilePosition{}, err
	}
	directory, backend, err := a.readable(ctx, position.Directory)
	if errors.Is(err, domain.ErrDirectoryNotOpen) {
		return nil, nil, domain.FilePosition{}, ErrFileNotFound
	}
	if err != nil {
		return nil, nil, domain.FilePosition{}, err
	}
	stored, err := backend.Stat(ctx, position)
	if err != nil {
		return nil, nil, domain.FilePosition{}, err
	}
	if !stored.Attributes.Written() {
		return nil, nil, domain.FilePosition{}, ErrFileNotFound
	}
	return directory, backend, position, nil
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
