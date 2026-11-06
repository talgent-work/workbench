package personalwork

import (
	"context"
	"errors"

	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// WorkFile names one file of a Work.
type WorkFile struct {
	WorkID string
	Kind   FileKind
	Name   string
}

// ownedWork is a Work and the definition it runs under.
type ownedWork struct {
	work       *domain.AgentWork
	definition *domain.AgentDefinition
}

// loadWork loads a Work for a reader of its files: the person, or the Agent
// reading its owner's Library.
func (p *PersonalWorks) loadWork(ctx context.Context, person Person, workID string) (ownedWork, error) {
	if person.libraryReader() == "" {
		return ownedWork{}, access.ErrAccessDenied
	}
	work, definition, err := p.app.WorkWithDefinition(ctx, workID)
	return ownedWork{work: work, definition: definition}, err
}

// ListFiles lists a Personal Work's files to its owner and Work Share
// holders; only the owner sees Share Links.
func (p *PersonalWorks) ListFiles(ctx context.Context, person Person, workID string) ([]SpaceFile, error) {
	owned, err := p.loadWork(ctx, person, workID)
	if err != nil {
		return nil, err
	}
	basis, err := owned.work.AdmitFileReader(owned.definition, person.UserID)
	if err != nil {
		return nil, denied(err)
	}
	if !owned.work.Personal() {
		return nil, domain.ErrNotPersonalWork
	}
	files, err := p.files.ListFiles(ctx, owned.work.ID)
	if err != nil || basis == domain.ReadAsOwner {
		return files, err
	}
	for i := range files {
		files[i].ShareLinks = nil
	}
	return files, nil
}

// changeableWork admits the owner's change to a Personal Work's files; an
// Agent changes none.
func (p *PersonalWorks) changeableWork(ctx context.Context, person Person, workID string) (*domain.AgentWork, error) {
	if person.self() == "" {
		return nil, access.ErrAccessDenied
	}
	owned, err := p.loadWork(ctx, person, workID)
	if err != nil {
		return nil, err
	}
	if err := owned.work.AdmitFileChange(owned.definition, person.UserID); err != nil {
		return nil, denied(err)
	}
	return owned.work, nil
}

// IssueAttachmentUpload issues the link through which the owner creates or
// replaces one Attachment.
func (p *PersonalWorks) IssueAttachmentUpload(ctx context.Context, person Person, workID, name string, replace bool, contentType string) (FileLink, error) {
	work, err := p.changeableWork(ctx, person, workID)
	if err != nil {
		return FileLink{}, err
	}
	return p.files.IssueAttachmentUpload(ctx, work.InputSource, work.ID, name, replace, contentType, person.UserID)
}

// RenameAttachment moves the owner's Attachment to a free name.
func (p *PersonalWorks) RenameAttachment(ctx context.Context, person Person, workID, name, newName string) error {
	work, err := p.changeableWork(ctx, person, workID)
	if err != nil {
		return err
	}
	return p.files.RenameAttachment(ctx, work.InputSource, work.ID, name, newName, person.UserID)
}

// DeleteFile deletes the owner's Attachment or Artifact.
func (p *PersonalWorks) DeleteFile(ctx context.Context, person Person, file WorkFile) error {
	work, err := p.changeableWork(ctx, person, file.WorkID)
	if err != nil {
		return err
	}
	return p.files.DeleteFile(ctx, work.InputSource, work.ID, file.Kind, file.Name, person.UserID)
}

// CreateShareLink lets the owner share one file of a Personal Work by link,
// Closed or not.
func (p *PersonalWorks) CreateShareLink(ctx context.Context, person Person, file WorkFile) (ShareLink, error) {
	owner, err := p.sharingOwner(ctx, person, file.WorkID)
	if err != nil {
		return ShareLink{}, err
	}
	return p.files.CreateShareLink(ctx, owner, file.Kind, file.Name, person.UserID)
}

// StopSharing revokes every Share Link to one file of the owner's Personal Work.
func (p *PersonalWorks) StopSharing(ctx context.Context, person Person, file WorkFile) error {
	owner, err := p.sharingOwner(ctx, person, file.WorkID)
	if err != nil {
		return err
	}
	return p.files.RevokeShareLinks(ctx, owner, file.Kind, file.Name, person.UserID)
}

// sharingOwner admits the owner's sharing of a Personal Work's file; an Agent
// shares none.
func (p *PersonalWorks) sharingOwner(ctx context.Context, person Person, workID string) (string, error) {
	if person.self() == "" {
		return "", access.ErrAccessDenied
	}
	owned, err := p.loadWork(ctx, person, workID)
	if err != nil {
		return "", err
	}
	if err := owned.work.AdmitFileSharing(owned.definition, person.UserID); err != nil {
		return "", denied(err)
	}
	return owned.work.ID, nil
}

// IssueAccessLink issues a short-lived link to read one file the person may
// read.
func (p *PersonalWorks) IssueAccessLink(ctx context.Context, person Person, file WorkFile, inline bool) (FileLink, error) {
	owner, err := p.readableOwner(ctx, person, file)
	if err != nil {
		return FileLink{}, err
	}
	return p.files.IssueAccessLink(ctx, owner, file.Kind, file.Name, inline)
}

// FilePreview tells how one readable file is shown; render first asks for
// its Preview Copy.
func (p *PersonalWorks) FilePreview(ctx context.Context, person Person, file WorkFile, render bool) (FilePreview, error) {
	owner, err := p.readableOwner(ctx, person, file)
	if err != nil {
		return FilePreview{}, err
	}
	return p.files.FilePreview(ctx, owner, file.Kind, file.Name, render)
}

// readableOwner admits the person to read one file and names its File
// Space's owner: the Work itself for a Personal Work, otherwise the owner its
// input source resolves to. An Agent reads its owner's Library, which holds
// Artifacts only.
func (p *PersonalWorks) readableOwner(ctx context.Context, person Person, file WorkFile) (string, error) {
	if person.Agent != nil && file.Kind != FileKindArtifact {
		return "", ErrFileNotFound
	}
	owned, err := p.loadWork(ctx, person, file.WorkID)
	if err != nil {
		return "", err
	}
	if _, err := owned.work.AdmitFileReader(owned.definition, person.UserID); err != nil {
		return "", denied(err)
	}
	if owned.work.Personal() {
		return owned.work.ID, nil
	}
	if file.Kind != FileKindArtifact {
		return "", ErrFileNotFound
	}
	owners, err := p.files.ResolveOwners(ctx, []string{owned.work.InputSource.Reference})
	if err != nil {
		return "", err
	}
	if len(owners) != 1 || owners[0] == "" {
		return "", domain.ErrWorkFilesUnavailable
	}
	files, err := p.files.ListFiles(ctx, owners[0])
	if err != nil {
		return "", err
	}
	for _, listed := range files {
		if listed.Kind == file.Kind && listed.Name == file.Name && domain.ReadableWorkFile(owned.work.ID, false, true, listed.WorkID) {
			return owners[0], nil
		}
	}
	return "", ErrFileNotFound
}

// denied reports a refused file access as an access denial.
func denied(err error) error {
	if errors.Is(err, domain.ErrWorkReaderNotAdmitted) || errors.Is(err, domain.ErrWorkFileNotAdmitted) {
		return errors.Join(access.ErrAccessDenied, err)
	}
	return err
}
