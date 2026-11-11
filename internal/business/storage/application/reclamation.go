package application

import (
	"context"
	"errors"
	"strings"

	"github.com/talgent-work/workbench/internal/business/storage/application/task"
	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

// RequestWorkReclamation accepts the reclamation of every Directory whose
// owner names a closed Work and whose lifetime is that Work's, at once, and
// queues the removal of each one's content. Agent requests it only once no
// environment mounts the Work's Directories. Repeating it changes nothing
// but queues the removals again, so a removal lost before it was queued is
// queued by the retry.
func (a *Application) RequestWorkReclamation(ctx context.Context, workID string) error {
	if strings.TrimSpace(workID) == "" {
		return ErrInvalidRequest
	}
	directories, err := a.directoryQueries.WorkDirectories(ctx, workID)
	if err != nil {
		return err
	}
	var failures []error
	for _, directory := range directories {
		if err := a.reclaim(ctx, directory.ID); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// reclaim records the reclamation at once. A mapped Directory's is
// recorded under its lock, removing its mappings' files and emptied folders
// from the Mapped View first, so none stays readable through a mapping once
// the record is gone. An unmapped one takes no lock: a publication copying
// from it holds its lock until the copy ends, and only the removal of its
// content waits for that. A mapping recorded in between leaves files no
// record holds, which the Mapped View repair removes.
func (a *Application) reclaim(ctx context.Context, id domain.DirectoryID) error {
	directory, err := a.directories.Get(ctx, id)
	if err != nil {
		return err
	}
	if !directory.Mapped() {
		if _, err := a.changeDirectory(ctx, id, (*domain.Directory).Reclaim); err != nil {
			return err
		}
		return a.requestRemoval(ctx, id)
	}
	backend, err := a.backends.of(directory)
	if err != nil {
		return err
	}
	err = a.mappedChange(ctx, backend, id, func(directory *domain.Directory) error {
		if err := a.endMappings(ctx, backend, directory); err != nil {
			return err
		}
		_, err := a.changeDirectory(ctx, id, (*domain.Directory).Reclaim)
		return err
	})
	if err != nil {
		return err
	}
	return a.requestRemoval(ctx, id)
}

// endMappings removes the files of each of the Directory's mappings under
// the Mapped View's lock; the caller holds the Directory's.
func (a *Application) endMappings(ctx context.Context, backend DirectoryBackendPort, directory *domain.Directory) error {
	if !directory.Mapped() {
		return nil
	}
	unlockView, err := backend.LockMappedView(ctx)
	if err != nil {
		return err
	}
	defer unlockView()
	for _, path := range directory.Mappings {
		if err := backend.UnmapFiles(ctx, path); err != nil {
			return err
		}
	}
	return nil
}

// requestRemoval queues the removal of a reclaimed Directory's content.
func (a *Application) requestRemoval(ctx context.Context, id domain.DirectoryID) error {
	return task.RequestContentRemoval(ctx, a.tasks.Maintenance, task.RemoveReclaimedContent{Directory: id}, directoryKey(id))
}

// RemoveReclaimedContent removes a reclaimed Directory's content once no
// publication copies from it; ErrPublicationInProgress leaves it for the
// next attempt. A Directory not reclaimed keeps its content.
func (a *Application) RemoveReclaimedContent(ctx context.Context, removal task.RemoveReclaimedContent) error {
	directory, err := a.directories.Get(ctx, removal.Directory)
	if errors.Is(err, domain.ErrDirectoryNotOpen) {
		return nil
	}
	if err != nil {
		return err
	}
	if !directory.Reclaimed() {
		return nil
	}
	backend, err := a.backends.of(directory)
	if err != nil {
		return err
	}
	return backend.RemoveReclaimed(ctx, directory.ID)
}
