package application

import (
	"bytes"
	"context"
	"io"
	"mime"
	"path"
	"strings"

	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

// ArtifactSource is the publishing Work, the Space Token of the File Space
// its Work was given, and what it publishes: a file of its Session Workspace,
// or, for a Work without one, the content it sent. Exactly one of Path and
// Content is set; an empty file sent is a non-nil empty Content. A Work with
// a Session Workspace publishes only from it.
type ArtifactSource struct {
	SpaceToken         string
	WorkID             string
	WorkspaceReference string
	// Path is relative to /workspace.
	Path    string
	Content []byte
}

// sent reports whether the source is content the Work sent.
func (s ArtifactSource) sent() bool { return s.Content != nil }

// PublishArtifact publishes one workspace file, or the content a Work sent,
// as a new Artifact, or over an existing one when replace is set, within the
// request. A retry meets the command's own semantics: a new Artifact refuses
// a taken name, and a replacement writes again.
func (a *Application) PublishArtifact(ctx context.Context, source ArtifactSource, name string, replace bool) (SpaceFile, error) {
	writer, err := domain.WorkWriter(source.WorkID)
	if err != nil || source.sent() && source.Path != "" || !source.sent() && strings.TrimSpace(source.WorkspaceReference) == "" {
		return SpaceFile{}, ErrInvalidRequest
	}
	if source.sent() && strings.TrimSpace(source.WorkspaceReference) != "" {
		return SpaceFile{}, domain.ErrSentContentWithWorkspace
	}
	if len(source.Content) > a.sentContentLimit {
		return SpaceFile{}, domain.ErrInvalidPublicationSource
	}
	space, err := a.tokenSpace(ctx, source.SpaceToken, writer)
	if err != nil {
		return SpaceFile{}, err
	}
	position, err := domain.NewFilePosition(space.Owner, domain.FileKindArtifact, name)
	if err != nil {
		return SpaceFile{}, err
	}
	location := space.Owner
	var stored StoredFile
	write := func(content io.Reader) error {
		var writeErr error
		stored, writeErr = a.writeFile(ctx, location, position, content, FileAttributes{WorkID: source.WorkID}, replace)
		return writeErr
	}
	if source.sent() {
		err = write(bytes.NewReader(source.Content))
	} else {
		err = a.publishWorkspaceFile(ctx, source, write)
	}
	if err != nil {
		return SpaceFile{}, err
	}
	a.fileChanged(ctx, location, FileChange{Kind: domain.FileKindArtifact, Name: position.Name, Operation: writtenOperation(replace), Writer: writer})
	return a.spaceFile(space, stored), nil
}

// publishWorkspaceFile hands write the Session Workspace file the source
// names.
func (a *Application) publishWorkspaceFile(ctx context.Context, source ArtifactSource, write func(io.Reader) error) error {
	sourcePath, err := domain.ParsePublicationSource(source.Path)
	if err != nil {
		return err
	}
	return a.working.ReadPublicationSource(ctx, source.WorkspaceReference, sourcePath, write)
}

// DeleteArtifact deletes an Artifact for the Work whose Agent holds the Space
// Token; deleting an absent Artifact succeeds, so a retry finds nothing left
// to do.
func (a *Application) DeleteArtifact(ctx context.Context, token, name, workID string) error {
	writer, err := domain.WorkWriter(workID)
	if err != nil {
		return err
	}
	space, err := a.tokenSpace(ctx, token, writer)
	if err != nil {
		return err
	}
	return a.deleteFile(ctx, space, domain.FileKindArtifact, name, writer)
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

// tokenSpace finds the File Space a Work Agent's Space Token was issued for
// and admits a write by writer on it.
func (a *Application) tokenSpace(ctx context.Context, token string, writer domain.FileWriter) (*domain.FileSpace, error) {
	if token == "" {
		return nil, domain.ErrSpaceTokenRejected
	}
	space, err := a.spaces.GetBySpaceToken(ctx, token)
	if err != nil {
		return nil, err
	}
	if err := space.AdmitFileWrite(token, writer); err != nil {
		return nil, err
	}
	return space, nil
}
