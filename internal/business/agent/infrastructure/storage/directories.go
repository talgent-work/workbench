package storage

import (
	"github.com/samber/oops"
	storageprivatev1 "github.com/talgent-work/contracts/gen/go/storage/private/v1"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

var backends = map[domain.DirectoryBackend]storageprivatev1.Backend{
	domain.BackendNAS:     storageprivatev1.Backend_BACKEND_NAS,
	domain.BackendJuiceFS: storageprivatev1.Backend_BACKEND_JUICEFS,
}

var lifetimes = map[domain.DirectoryLifetime]storageprivatev1.Lifetime{
	domain.LifetimeWork:      storageprivatev1.Lifetime_LIFETIME_WORK,
	domain.LifetimePermanent: storageprivatev1.Lifetime_LIFETIME_PERMANENT,
}

func directoryToProto(directory domain.Directory) *storageprivatev1.Directory {
	owner := &storageprivatev1.DirectoryOwner{Facts: make([]*storageprivatev1.OwnerFact, 0, len(directory.Owner))}
	for _, fact := range directory.Owner {
		owner.Facts = append(owner.Facts, &storageprivatev1.OwnerFact{Condition: string(fact.Condition), Value: fact.Value})
	}
	return &storageprivatev1.Directory{Owner: owner, Name: directory.Name}
}

func directoryFromProto(directory *storageprivatev1.Directory) domain.Directory {
	result := domain.Directory{Name: directory.GetName()}
	for _, fact := range directory.GetOwner().GetFacts() {
		result.Owner = append(result.Owner, domain.OwnerFact{Condition: domain.LocatorCondition(fact.GetCondition()), Value: fact.GetValue()})
	}
	return result
}

// locatedToProto names a located Directory with its Backend and Lifetime. A
// Backend or Lifetime Storage's contract does not know is refused rather
// than sent as unspecified, which Storage would have to guess at.
func locatedToProto(directory domain.LocatedDirectory) (*storageprivatev1.LocatedDirectory, error) {
	backend, known := backends[directory.Backend]
	if !known {
		return nil, oops.With("backend", directory.Backend).Errorf("agent: Storage has no Directory Backend %q", directory.Backend)
	}
	lifetime, known := lifetimes[directory.Lifetime]
	if !known {
		return nil, oops.With("lifetime", directory.Lifetime).Errorf("agent: Storage has no Directory Lifetime %q", directory.Lifetime)
	}
	return &storageprivatev1.LocatedDirectory{Directory: directoryToProto(directory.Directory), Backend: backend, Lifetime: lifetime}, nil
}

func positionToProto(directory domain.Directory, name string) *storageprivatev1.FilePosition {
	return &storageprivatev1.FilePosition{Directory: directoryToProto(directory), Name: name}
}

// workspaceLayoutToProto names a Session Workspace and the Directories
// mounted in it, as the Work's sandbox sees them.
func workspaceLayoutToProto(layout domain.WorkspaceLayout) *storageprivatev1.WorkspaceLayout {
	result := &storageprivatev1.WorkspaceLayout{Workspace: directoryToProto(layout.Workspace)}
	for _, mount := range layout.Mounts {
		result.Mounts = append(result.Mounts, &storageprivatev1.MountedDirectory{Path: mount.Path, Directory: directoryToProto(mount.Directory)})
	}
	return result
}
