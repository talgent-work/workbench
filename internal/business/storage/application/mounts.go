package application

import (
	"context"
	"strings"

	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

// MountRequest is one located Directory a Work's sandbox mounts: read-only
// or read-write, with the mount points inside it where Agent mounts other
// Directories.
type MountRequest struct {
	Directory   Located
	ReadOnly    bool
	MountPoints []string
}

// MountGrant is a Directory's location and access mode, opaque to its
// caller.
type MountGrant struct {
	ClaimName string
	SubPath   string
	ReadOnly  bool
}

// AcquireMounts opens each Directory of a Work's located layout, completing
// its allocation, prepares the mount points named inside it with every
// parent owned by the sandbox user, and returns one Mount Grant per
// Directory in request order. Nothing is granted for a Directory before its
// space exists. A grant is issued, never held: there is no unmount.
func (a *Application) AcquireMounts(ctx context.Context, workID string, requests []MountRequest) ([]MountGrant, error) {
	if strings.TrimSpace(workID) == "" {
		return nil, ErrInvalidRequest
	}
	located := make([]domain.LocatedDirectory, 0, len(requests))
	seen := make(map[domain.DirectoryID]bool, len(requests))
	for _, request := range requests {
		directory, err := request.Directory.located()
		if err != nil {
			return nil, err
		}
		// A Work mounts no other Work's Directory.
		if owner, ok := directory.ID.Owner.Work(); seen[directory.ID] || ok && owner != workID {
			return nil, ErrInvalidRequest
		}
		seen[directory.ID] = true
		located = append(located, directory)
	}
	grants := make([]MountGrant, 0, len(requests))
	for i, request := range requests {
		grant, err := a.grantMount(ctx, located[i], request)
		if err != nil {
			return nil, err
		}
		grants = append(grants, grant)
	}
	return grants, nil
}

func (a *Application) grantMount(ctx context.Context, located domain.LocatedDirectory, request MountRequest) (MountGrant, error) {
	directory, err := a.open(ctx, located)
	if err != nil {
		return MountGrant{}, err
	}
	var admission domain.MountAdmission
	if !directory.RecordsReadWrite(request.ReadOnly) {
		admission, err = directory.GrantMount(request.ReadOnly, request.MountPoints)
	} else {
		// The first read-write grant is recorded before the mount exists, so
		// a mapping and it exclude each other through the Directory's
		// version.
		directory, err = a.changeDirectory(ctx, located.ID, func(directory *domain.Directory) error {
			var grantErr error
			admission, grantErr = directory.GrantMount(request.ReadOnly, request.MountPoints)
			return grantErr
		})
	}
	if err != nil {
		return MountGrant{}, err
	}
	backend, err := a.backends.of(directory)
	if err != nil {
		return MountGrant{}, err
	}
	if err := backend.PrepareMountPoints(ctx, admission.Directory, admission.MountPoints); err != nil {
		return MountGrant{}, err
	}
	location := backend.Locate(admission.Directory)
	return MountGrant{ClaimName: location.ClaimName, SubPath: location.SubPath, ReadOnly: admission.ReadOnly}, nil
}
