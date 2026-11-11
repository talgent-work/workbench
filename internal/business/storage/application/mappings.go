package application

import (
	"context"
	"errors"

	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

// The Mapped View: a caller maps a located Directory at a mapped path
// beneath its mapping prefix, and Storage shows the Directory's files there
// as the same files. Every change of a Directory's mappings and files runs
// under its lock, so a mapping's files follow its record: a mapping is
// recorded before its files appear, and its files go before its record.
// Mapping and unmapping also hold the Mapped View's lock, taken after the
// Directory's, so the paths found disjoint stay disjoint until recorded and
// no folder one mapping empties is removed under another being made.

// MapDirectory maps a located Directory at a mapped path beneath the
// caller's prefix, opening the Directory first when it was never opened.
// Mapping it again at the same path changes nothing but aligns its files.
func (a *Application) MapDirectory(ctx context.Context, request Located, rawPath string, prefix domain.MappingPrefix) error {
	path, err := domain.ParseMappedPath(rawPath)
	if err != nil {
		return err
	}
	// A caller outside its prefix opens nothing.
	if err := prefix.Admit(path); err != nil {
		return err
	}
	located, err := request.located()
	if err != nil {
		return err
	}
	if err := located.AdmitMapping(); err != nil {
		return err
	}
	directory, err := a.open(ctx, located)
	if err != nil {
		return err
	}
	return a.underLock(ctx, directory, func(backend DirectoryBackendPort) error {
		unlockView, err := backend.LockMappedView(ctx)
		if err != nil {
			return err
		}
		defer unlockView()
		around, err := a.directoryQueries.MappingsAround(ctx, path)
		if err != nil {
			return err
		}
		disjoint := true
		for _, mapping := range around {
			if mapping.Directory != located.ID || mapping.Path != path {
				disjoint = false
			}
		}
		if _, err := a.changeDirectory(ctx, located.ID, func(directory *domain.Directory) error {
			return directory.Map(path, prefix, disjoint)
		}); err != nil {
			return err
		}
		return backend.MapFiles(ctx, located.ID, path)
	})
}

// UnmapDirectory removes a Directory's mapping at a mapped path beneath the
// caller's prefix: its files and emptied folders first, then its record, so
// an interruption leaves the mapping recorded with fewer files. Removing a
// mapping the Directory does not have, or one of a Directory never opened or
// reclaimed, changes nothing.
func (a *Application) UnmapDirectory(ctx context.Context, named Named, rawPath string, prefix domain.MappingPrefix) error {
	path, err := domain.ParseMappedPath(rawPath)
	if err != nil {
		return err
	}
	if err := prefix.Admit(path); err != nil {
		return err
	}
	directory, _, err := a.readableNamed(ctx, named)
	if errors.Is(err, domain.ErrDirectoryNotOpen) || errors.Is(err, domain.ErrDirectoryReclaimed) {
		return nil
	}
	if err != nil {
		return err
	}
	return a.underLock(ctx, directory, func(backend DirectoryBackendPort) error {
		current, err := a.directories.Get(ctx, directory.ID)
		if err != nil {
			return err
		}
		if !current.MappedAt(path) {
			return nil
		}
		unlockView, err := backend.LockMappedView(ctx)
		if err != nil {
			return err
		}
		defer unlockView()
		if err := backend.UnmapFiles(ctx, path); err != nil {
			return err
		}
		_, err = a.changeDirectory(ctx, directory.ID, func(directory *domain.Directory) error {
			return directory.Unmap(path, prefix)
		})
		return err
	})
}

// underLock runs change holding the Directory's lock on its backend, across
// every controlplane replica.
func (a *Application) underLock(ctx context.Context, directory *domain.Directory, change func(DirectoryBackendPort) error) error {
	backend, err := a.backends.of(directory)
	if err != nil {
		return err
	}
	unlock, err := backend.Lock(ctx, directory.ID)
	if err != nil {
		return err
	}
	defer unlock()
	return change(backend)
}
