package connectrpc

import (
	agentprivatev1 "github.com/talgent-work/contracts/gen/go/agent/private/v1"
	storageprivatev1 "github.com/talgent-work/contracts/gen/go/storage/private/v1"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// subjectFromProto maps an optional Work Subject; an absent one is none.
func subjectFromProto(value *agentprivatev1.WorkSubject) domain.WorkSubject {
	return domain.WorkSubject{Kind: domain.SubjectKind(value.GetKind()), ID: value.GetId()}
}

// subjectToProto omits a Work's absent Subject.
func subjectToProto(value domain.WorkSubject) *agentprivatev1.WorkSubject {
	if !value.Present() {
		return nil
	}
	return &agentprivatev1.WorkSubject{Kind: string(value.Kind), Id: value.ID}
}

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
