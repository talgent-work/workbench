package application

import (
	"context"

	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

// The Owner File Query and the resolution of input sources to owners let a
// caller read the files of spaces it names, after deciding its reader may
// see them.

// maxOwnerFileQuery bounds the owners one Owner File Query names.
const maxOwnerFileQuery = 1000

// ListOwnerFiles is the Owner File Query: the files in the File Spaces of
// the given owners, owner by owner as given. The caller names only owners
// whose files it authorized its reader to see; an owner whose space never
// opened has none.
func (a *Application) ListOwnerFiles(ctx context.Context, owners []string) ([]StoredFile, error) {
	if len(owners) > maxOwnerFileQuery {
		return nil, ErrInvalidRequest
	}
	seen := make(map[string]bool, len(owners))
	var files []StoredFile
	for _, raw := range owners {
		owner, err := domain.ParseOwner(raw)
		if err != nil {
			return nil, err
		}
		if seen[owner] {
			continue
		}
		seen[owner] = true
		stored, err := a.persistent.List(ctx, owner)
		if err != nil {
			return nil, err
		}
		files = append(files, stored...)
	}
	return files, nil
}

// ResolveInputSources names the owner of the File Space each input source
// opens, in order; a source that names no File Space resolves to no owner.
func (a *Application) ResolveInputSources(inputSources []string) ([]string, error) {
	if len(inputSources) > maxOwnerFileQuery {
		return nil, ErrInvalidRequest
	}
	owners := make([]string, len(inputSources))
	for i, source := range inputSources {
		if owner, err := domain.OwnerOfInputSource(source); err == nil {
			owners[i] = owner
		}
	}
	return owners, nil
}
