package connectrpc

import (
	storageprivatev1 "github.com/talgent-work/contracts/gen/go/storage/private/v1"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// A verified Work identity carries the Work's Session Workspace in Storage's
// own contract message, which the Work's Agent hands Storage unchanged. The
// same mapping exists in the Storage ACL (infrastructure/storage), which
// sends it on Agent's behalf; Transport does not import Infrastructure, so
// each layer keeps its own copy of this mechanical mapping.

// workspaceLayoutToProto maps a Work's Session Workspace as its sandbox sees
// it; a Work that mounts none has none.
func workspaceLayoutToProto(layout *domain.WorkspaceLayout) *storageprivatev1.WorkspaceLayout {
	if layout == nil {
		return nil
	}
	result := &storageprivatev1.WorkspaceLayout{Workspace: directoryToProto(layout.Workspace)}
	for _, mount := range layout.Mounts {
		result.Mounts = append(result.Mounts, &storageprivatev1.MountedDirectory{Path: mount.Path, Directory: directoryToProto(mount.Directory)})
	}
	return result
}

func directoryToProto(directory domain.Directory) *storageprivatev1.Directory {
	owner := &storageprivatev1.DirectoryOwner{}
	for _, fact := range directory.Owner {
		owner.Facts = append(owner.Facts, &storageprivatev1.OwnerFact{Condition: string(fact.Condition), Value: fact.Value})
	}
	return &storageprivatev1.Directory{Owner: owner, Name: directory.Name}
}
