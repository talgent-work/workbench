package application

import (
	"context"
	"errors"

	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

// Located is a located Directory as a caller names it: the Directory owner's
// facts and name with the backend and lifetime of its first allocation.
type Located struct {
	Directory Named
	Backend   domain.Backend
	Lifetime  domain.Lifetime
}

// Named is a Directory as a caller names it: its owner's facts in condition
// order and its name.
type Named struct {
	Owner []domain.OwnerFact
	Name  string
}

// ID admits the Directory's identity.
func (n Named) ID() (domain.DirectoryID, error) {
	owner, err := domain.NewDirectoryOwner(n.Owner)
	if err != nil {
		return domain.DirectoryID{}, err
	}
	return domain.NewDirectoryID(owner, n.Name)
}

func (l Located) located() (domain.LocatedDirectory, error) {
	id, err := l.Directory.ID()
	if err != nil {
		return domain.LocatedDirectory{}, err
	}
	return domain.NewLocatedDirectory(id, l.Backend, l.Lifetime)
}

// OpenDirectory opens a located Directory on its first need, or returns the
// existing one with its Space Token unchanged, completing an allocation that
// failed half-way. The first opening fixes backend and lifetime.
func (a *Application) OpenDirectory(ctx context.Context, request Located) (string, error) {
	located, err := request.located()
	if err != nil {
		return "", err
	}
	directory, err := a.open(ctx, located)
	if err != nil {
		return "", err
	}
	return directory.SpaceToken, nil
}

// open returns the Directory a located Directory names, establishing it on
// its first opening and allocating its space while it awaits it, so it is
// Allocated on return.
func (a *Application) open(ctx context.Context, located domain.LocatedDirectory) (*domain.Directory, error) {
	directory, err := a.directories.Get(ctx, located.ID)
	if errors.Is(err, domain.ErrDirectoryNotOpen) {
		directory, err = a.establish(ctx, located)
	}
	if err != nil {
		return nil, err
	}
	if err := directory.Open(); err != nil {
		return nil, err
	}
	if !directory.AwaitsAllocation() {
		return directory, nil
	}
	backend, err := a.backends.of(directory)
	if err != nil {
		return nil, err
	}
	// Should allocation fail, the Directory stays Allocating and the next
	// opening allocates again; an existing space is kept.
	if err := backend.Allocate(ctx, directory.ID); err != nil {
		return nil, err
	}
	allocated, err := a.changeDirectory(ctx, directory.ID, (*domain.Directory).RecordAllocation)
	if errors.Is(err, domain.ErrDirectoryReclaimed) {
		// Reclaimed while its space was being allocated: its removal may
		// already have run, so the space just allocated is removed again.
		return nil, errors.Join(err, a.requestRemoval(ctx, directory.ID))
	}
	return allocated, err
}

// establish records a new Directory; a concurrent opening that won holds the
// Directory and its token.
func (a *Application) establish(ctx context.Context, located domain.LocatedDirectory) (*domain.Directory, error) {
	directory, err := domain.NewDirectory(located)
	if err != nil {
		return nil, err
	}
	err = a.directories.Save(ctx, directory)
	if errors.Is(err, domain.ErrConcurrentModification) {
		return a.directories.Get(ctx, located.ID)
	}
	return directory, err
}

// directoryChangeAttempts bounds retries of a Directory change racing another.
const directoryChangeAttempts = 5

// changeDirectory applies one change to the current Directory, retrying on a
// concurrent change of the same Directory so its changes stay serialized.
func (a *Application) changeDirectory(ctx context.Context, id domain.DirectoryID, change func(*domain.Directory) error) (*domain.Directory, error) {
	for attempt := 1; ; attempt++ {
		directory, err := a.directories.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		if err := change(directory); err != nil {
			return nil, err
		}
		err = a.directories.Save(ctx, directory)
		if err == nil {
			return directory, nil
		}
		if !errors.Is(err, domain.ErrConcurrentModification) || attempt == directoryChangeAttempts {
			return nil, err
		}
	}
}

// readable loads a Directory a reader names with its backend; one never
// opened returns ErrDirectoryNotOpen and a reclaimed one
// ErrDirectoryReclaimed.
func (a *Application) readable(ctx context.Context, id domain.DirectoryID) (*domain.Directory, DirectoryBackendPort, error) {
	directory, err := a.directories.Get(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if err := directory.AdmitRead(); err != nil {
		return nil, nil, err
	}
	backend, err := a.backends.of(directory)
	if err != nil {
		return nil, nil, err
	}
	return directory, backend, nil
}

// readableNamed is readable for a Directory as a caller names it.
func (a *Application) readableNamed(ctx context.Context, named Named) (*domain.Directory, DirectoryBackendPort, error) {
	id, err := named.ID()
	if err != nil {
		return nil, nil, err
	}
	return a.readable(ctx, id)
}
