package application

import (
	"context"
	"errors"
	"log/slog"

	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

// RepairMappedView aligns every mapped path with its Directory's files,
// linking what an interrupted change or unmapping left missing, then
// removes whatever no mapping holds, such as folders an interrupted removal
// left empty or entries no mapping ever made. Each mapping is aligned under its Directory's lock and the
// Mapped View's, as a mapping is made, so writes go on meanwhile; one
// failing mapping leaves the others repaired and fails the run, which the
// next hour's replaces.
func (a *Application) RepairMappedView(ctx context.Context) error {
	backend, ok := a.backends[domain.BackendJuiceFS]
	if !ok {
		return nil
	}
	mappings, err := a.directoryQueries.Mappings(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, mapping := range mappings {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := a.repairMapping(ctx, backend, mapping.Directory, mapping.Path); err != nil {
			a.logger.WarnContext(ctx, "mapped path not repaired", slog.String("mapped_path", string(mapping.Path)), slog.Any("error", err))
			failures = append(failures, err)
		}
	}
	if err := a.pruneMappedView(ctx, backend); err != nil {
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}

// repairMapping aligns one mapped path while its Directory still has it.
func (a *Application) repairMapping(ctx context.Context, backend DirectoryBackendPort, id domain.DirectoryID, path domain.MappedPath) error {
	return a.mappedChange(ctx, backend, id, func(directory *domain.Directory) error {
		if !directory.MappedAt(path) {
			return nil
		}
		unlockView, err := backend.LockMappedView(ctx)
		if err != nil {
			return err
		}
		defer unlockView()
		return backend.MapFiles(ctx, id, path)
	})
}

// pruneMappedView removes what no mapping holds, reading the mappings under
// the Mapped View's lock: a mapping is recorded only under it, so none is
// made while the view is pruned.
func (a *Application) pruneMappedView(ctx context.Context, backend DirectoryBackendPort) error {
	unlockView, err := backend.LockMappedView(ctx)
	if err != nil {
		return err
	}
	defer unlockView()
	mappings, err := a.directoryQueries.Mappings(ctx)
	if err != nil {
		return err
	}
	paths := make([]domain.MappedPath, 0, len(mappings))
	for _, mapping := range mappings {
		paths = append(paths, mapping.Path)
	}
	return backend.PruneMappedView(ctx, paths)
}
