package application

import (
	"bytes"
	"context"
	"io"

	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

// ArtifactSource is the publishing Work, the Space Token of the File Space
// it publishes into, and what it publishes: a file of its Session Workspace,
// or, for a Work without one, the content it sent. Exactly one of Path and
// Content is set; an empty file sent is a non-nil empty Content. A Work with
// a Session Workspace publishes only from it.
type ArtifactSource struct {
	SpaceToken string
	WorkID     string
	// Workspace is the Work's Session Workspace as its sandbox sees it, nil
	// for a Work without one.
	Workspace *Layout
	// Path is relative to the Work Directory.
	Path    string
	Content []byte
}

// sent reports whether the source is content the Work sent.
func (s ArtifactSource) sent() bool { return s.Content != nil }

// PublishArtifact publishes one workspace file, or the content a Work sent,
// as a new Artifact, or over an existing one when replace is set, within the
// request. A retry meets the command's own semantics: a new Artifact refuses
// a taken name, and a replacement writes again.
func (a *Application) PublishArtifact(ctx context.Context, source ArtifactSource, name string, replace bool) (DirectoryFile, error) {
	writer, err := domain.WorkWriter(source.WorkID)
	if err != nil || source.sent() && source.Path != "" || !source.sent() && source.Workspace == nil {
		return DirectoryFile{}, ErrInvalidRequest
	}
	if source.sent() && source.Workspace != nil {
		return DirectoryFile{}, domain.ErrSentContentWithWorkspace
	}
	if len(source.Content) > a.sentContentLimit {
		return DirectoryFile{}, domain.ErrInvalidPublicationSource
	}
	directory, backend, err := a.tokenDirectory(ctx, source.SpaceToken, writer)
	if err != nil {
		return DirectoryFile{}, err
	}
	position, err := domain.NewFilePosition(directory.ID, name)
	if err != nil {
		return DirectoryFile{}, err
	}
	var stored StoredFile
	write := func(content io.Reader) error {
		var writeErr error
		stored, writeErr = a.writeFile(ctx, backend, position, content, FileAttributes{WorkID: writer.WorkID()}, replace)
		return writeErr
	}
	if source.sent() {
		err = write(bytes.NewReader(source.Content))
	} else {
		err = a.copyWorkspaceFile(ctx, *source.Workspace, source.Path, write)
	}
	if err != nil {
		return DirectoryFile{}, err
	}
	a.fileChanged(ctx, FileChange{Directory: directory.ID, Name: position.Name, Operation: writtenOperation(replace), Writer: writer})
	return a.directoryFile(directory, stored), nil
}

// copyWorkspaceFile hands write the Session Workspace file the source names:
// one regular file of the Work Directory outside every Directory mounted in
// it, copied under the Work Directory's shared lock so its reclamation waits.
func (a *Application) copyWorkspaceFile(ctx context.Context, workspace Layout, raw string, write func(io.Reader) error) error {
	layout, err := workspace.layout()
	if err != nil {
		return err
	}
	source, err := layout.PublicationSource(raw)
	if err != nil {
		return err
	}
	_, backend, err := a.readable(ctx, layout.Workspace)
	if err != nil {
		return err
	}
	return backend.CopyPublicationSource(ctx, layout.Workspace, source, write)
}

// DeleteArtifact deletes an Artifact for the Work whose Agent holds the Space
// Token; deleting an absent Artifact succeeds, so a retry finds nothing left
// to do.
func (a *Application) DeleteArtifact(ctx context.Context, token, name, workID string) error {
	writer, err := domain.WorkWriter(workID)
	if err != nil {
		return err
	}
	directory, backend, err := a.tokenDirectory(ctx, token, writer)
	if err != nil {
		return err
	}
	position, err := domain.NewFilePosition(directory.ID, name)
	if err != nil {
		return err
	}
	return a.deleteFile(ctx, backend, position, writer)
}

// tokenDirectory finds the File Space a Work Agent's Space Token was issued
// for and admits a write by writer on it.
func (a *Application) tokenDirectory(ctx context.Context, token string, writer domain.FileWriter) (*domain.Directory, DirectoryBackendPort, error) {
	if token == "" {
		return nil, nil, domain.ErrSpaceTokenRejected
	}
	directory, err := a.directories.GetBySpaceToken(ctx, token)
	if err != nil {
		return nil, nil, err
	}
	if err := directory.AdmitFileWrite(token, writer); err != nil {
		return nil, nil, err
	}
	backend, err := a.backends.of(directory)
	return directory, backend, err
}
