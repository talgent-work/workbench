package application

import (
	"context"
	"time"

	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

// FileOperation is how a file in a File Space changed.
type FileOperation string

const (
	FileCreated  FileOperation = "created"
	FileReplaced FileOperation = "replaced"
	FileRenamed  FileOperation = "renamed"
	FileDeleted  FileOperation = "deleted"
)

// FileChange is one committed change of a file in a File Space: its owner,
// its writer and which file, never its content.
type FileChange struct {
	Owner string
	Kind  domain.FileKind
	// Name is the file's name after the change; PreviousName is the former
	// name of a renamed Attachment.
	Name, PreviousName string
	Operation          FileOperation
	Writer             domain.FileWriter
	OccurredAt         time.Time
}

// FileChangesPort publishes each committed file change for others to
// observe, best effort: a lost publication never undoes or fails
// the change.
type FileChangesPort interface {
	Publish(ctx context.Context, change FileChange)
}

// fileChanged publishes a committed change of a file in space.
func (a *Application) fileChanged(ctx context.Context, owner string, change FileChange) {
	if a.fileChanges == nil {
		return
	}
	change.Owner = owner
	change.OccurredAt = a.now().UTC()
	a.fileChanges.Publish(ctx, change)
}

// writtenOperation is the operation a committed write was.
func writtenOperation(replace bool) FileOperation {
	if replace {
		return FileReplaced
	}
	return FileCreated
}
