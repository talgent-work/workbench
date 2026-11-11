// Package query holds Storage's Query Repository contracts and their read
// models.
package query

import (
	"context"
	"time"

	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

// WorkDirectory is one Directory facts Work reclamation reads: its owner,
// name, backend, lifetime and state.
type WorkDirectory struct {
	ID       domain.DirectoryID
	Backend  domain.Backend
	Lifetime domain.Lifetime
	State    domain.DirectoryState
}

// Mapping is one Directory Mapping: a mapped path with its Directory.
type Mapping struct {
	Path      domain.MappedPath
	Directory domain.DirectoryID
}

// DirectoryQueryRepository reads Directory facts the Directory Repository's
// Get does not serve.
type DirectoryQueryRepository interface {
	// WorkDirectories are the Directories whose owner names the Work and
	// whose lifetime is that Work's, whatever their state.
	WorkDirectories(ctx context.Context, workID string) ([]WorkDirectory, error)
	// MappingsAround are the mappings whose paths are, lie inside or lie
	// above the path, with their Directories.
	MappingsAround(ctx context.Context, path domain.MappedPath) ([]Mapping, error)
}

// PreviewCopyQueryRepository reads Preview Copy facts the Preview Copy
// Repository's Get does not serve.
type PreviewCopyQueryRepository interface {
	// MadeBefore are the keys of the Preview Copies and recorded failures
	// made, and the renderings requested, before the time.
	MadeBefore(ctx context.Context, before time.Time) ([]domain.PreviewKey, error)
}
