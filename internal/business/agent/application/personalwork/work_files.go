package personalwork

import (
	"context"
	"errors"

	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// WorkFile names one file of a Work.
type WorkFile struct {
	WorkID string
	Kind   command.FileKind
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
// holders, its Attachments then its Artifacts; only the owner sees Share
// Links.
func (p *PersonalWorks) ListFiles(ctx context.Context, person Person, workID string) ([]command.SpaceFile, error) {
	owned, err := p.loadWork(ctx, person, workID)
	if err != nil {
		return nil, err
	}
	basis, err := owned.work.AdmitFileReader(owned.definition, person.UserID)
	if err != nil {
		return nil, denied(err)
	}
	spaces, err := owned.work.PersonalFileSpaces()
	if errors.Is(err, domain.ErrWorkFilesUnavailable) && !owned.work.Personal() {
		return nil, domain.ErrNotPersonalWork
	}
	if err != nil {
		return nil, err
	}
	var files []command.SpaceFile
	for _, space := range []domain.LocatedDirectory{spaces.Inputs, spaces.Artifacts} {
		listed, err := p.files.ListFiles(ctx, space.Directory)
		if err != nil {
			return nil, err
		}
		files = append(files, listed...)
	}
	if basis != domain.ReadAsOwner {
		for i := range files {
			files[i].ShareLinks = nil
		}
	}
	return files, nil
}

// changeableSpace admits the owner's change to a Personal Work's file of the
// kind and opens the File Space holding it for its Space Token; an Agent
// changes none.
func (p *PersonalWorks) changeableSpace(ctx context.Context, person Person, workID string, kind command.FileKind) (domain.Directory, string, error) {
	if person.self() == "" {
		return domain.Directory{}, "", access.ErrAccessDenied
	}
	owned, err := p.loadWork(ctx, person, workID)
	if err != nil {
		return domain.Directory{}, "", err
	}
	if err := owned.work.AdmitFileChange(owned.definition, person.UserID); err != nil {
		return domain.Directory{}, "", denied(err)
	}
	space, err := personalSpace(owned.work, kind)
	if err != nil {
		return domain.Directory{}, "", err
	}
	token, err := p.files.OpenDirectory(ctx, space)
	return space.Directory, token, err
}

// personalSpace is the Personal Work's File Space holding files of the kind:
// its inputs for Attachments, its publication target for Artifacts.
func personalSpace(work *domain.AgentWork, kind command.FileKind) (domain.LocatedDirectory, error) {
	spaces, err := work.PersonalFileSpaces()
	if err != nil {
		return domain.LocatedDirectory{}, err
	}
	switch kind {
	case command.FileKindAttachment:
		return spaces.Inputs, nil
	case command.FileKindArtifact:
		return spaces.Artifacts, nil
	}
	return domain.LocatedDirectory{}, command.ErrFileNotFound
}

// IssueAttachmentUpload issues the link through which the owner creates or
// replaces one Attachment.
func (p *PersonalWorks) IssueAttachmentUpload(ctx context.Context, person Person, workID, name string, replace bool, contentType string) (command.FileLink, error) {
	space, token, err := p.changeableSpace(ctx, person, workID, command.FileKindAttachment)
	if err != nil {
		return command.FileLink{}, err
	}
	return p.files.IssueUpload(ctx, command.FilePosition{Directory: space, Name: name}, token, replace, contentType, person.UserID)
}

// RenameAttachment moves the owner's Attachment to a free name.
func (p *PersonalWorks) RenameAttachment(ctx context.Context, person Person, workID, name, newName string) error {
	space, token, err := p.changeableSpace(ctx, person, workID, command.FileKindAttachment)
	if err != nil {
		return err
	}
	return p.files.RenameFile(ctx, command.FilePosition{Directory: space, Name: name}, token, newName, person.UserID)
}

// DeleteFile deletes the owner's Attachment or Artifact.
func (p *PersonalWorks) DeleteFile(ctx context.Context, person Person, file WorkFile) error {
	space, token, err := p.changeableSpace(ctx, person, file.WorkID, file.Kind)
	if err != nil {
		return err
	}
	return p.files.DeleteFile(ctx, command.FilePosition{Directory: space, Name: file.Name}, token, person.UserID)
}

// CreateShareLink lets the owner share one file of a Personal Work by link,
// Closed or not.
func (p *PersonalWorks) CreateShareLink(ctx context.Context, person Person, file WorkFile) (command.ShareLink, error) {
	position, err := p.sharingPosition(ctx, person, file)
	if err != nil {
		return command.ShareLink{}, err
	}
	return p.files.CreateShareLink(ctx, position, person.UserID)
}

// StopSharing revokes every Share Link to one file of the owner's Personal Work.
func (p *PersonalWorks) StopSharing(ctx context.Context, person Person, file WorkFile) error {
	position, err := p.sharingPosition(ctx, person, file)
	if err != nil {
		return err
	}
	return p.files.RevokeShareLinks(ctx, position, person.UserID)
}

// sharingPosition admits the owner's sharing of a Personal Work's file and
// names its File position; an Agent shares none.
func (p *PersonalWorks) sharingPosition(ctx context.Context, person Person, file WorkFile) (command.FilePosition, error) {
	if person.self() == "" {
		return command.FilePosition{}, access.ErrAccessDenied
	}
	owned, err := p.loadWork(ctx, person, file.WorkID)
	if err != nil {
		return command.FilePosition{}, err
	}
	if err := owned.work.AdmitFileSharing(owned.definition, person.UserID); err != nil {
		return command.FilePosition{}, denied(err)
	}
	space, err := personalSpace(owned.work, file.Kind)
	if err != nil {
		return command.FilePosition{}, err
	}
	return command.FilePosition{Directory: space.Directory, Name: file.Name}, nil
}

// IssueAccessLink issues a short-lived link to read one file the person may
// read.
func (p *PersonalWorks) IssueAccessLink(ctx context.Context, person Person, file WorkFile, inline bool) (command.FileLink, error) {
	position, err := p.readablePosition(ctx, person, file)
	if err != nil {
		return command.FileLink{}, err
	}
	return p.files.IssueAccessLink(ctx, position, inline)
}

// FilePreview tells how one readable file is shown; render first asks for
// its Preview Copy.
func (p *PersonalWorks) FilePreview(ctx context.Context, person Person, file WorkFile, render bool) (command.FilePreview, error) {
	position, err := p.readablePosition(ctx, person, file)
	if err != nil {
		return command.FilePreview{}, err
	}
	return p.files.FilePreview(ctx, position, render)
}

// readablePosition admits the person to read one file and names its File
// position: in a Personal Work's own File Spaces, or, for another Work, an
// Artifact it published itself into its publication target. An Agent reads
// its owner's Library, which holds Artifacts only.
func (p *PersonalWorks) readablePosition(ctx context.Context, person Person, file WorkFile) (command.FilePosition, error) {
	if person.Agent != nil && file.Kind != command.FileKindArtifact {
		return command.FilePosition{}, command.ErrFileNotFound
	}
	owned, err := p.loadWork(ctx, person, file.WorkID)
	if err != nil {
		return command.FilePosition{}, err
	}
	if _, err := owned.work.AdmitFileReader(owned.definition, person.UserID); err != nil {
		return command.FilePosition{}, denied(err)
	}
	if owned.work.Personal() {
		space, err := personalSpace(owned.work, file.Kind)
		if err != nil {
			return command.FilePosition{}, err
		}
		return command.FilePosition{Directory: space.Directory, Name: file.Name}, nil
	}
	if file.Kind != command.FileKindArtifact {
		return command.FilePosition{}, command.ErrFileNotFound
	}
	target := owned.work.LocatedLayout.PublicationTarget.Directory
	files, err := p.files.ListFiles(ctx, target)
	if err != nil {
		return command.FilePosition{}, err
	}
	for _, listed := range files {
		if listed.Kind == file.Kind && listed.Name == file.Name && domain.ReadableWorkFile(owned.work.ID, false, true, listed.WorkID) {
			return command.FilePosition{Directory: target, Name: file.Name}, nil
		}
	}
	return command.FilePosition{}, command.ErrFileNotFound
}

// denied reports a refused file access as an access denial.
func denied(err error) error {
	if errors.Is(err, domain.ErrWorkReaderNotAdmitted) || errors.Is(err, domain.ErrWorkFileNotAdmitted) {
		return errors.Join(access.ErrAccessDenied, err)
	}
	return err
}
