package infrastructure

import (
	"path"

	"github.com/talgent-work/workbench/internal/business/storage/application"
	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

// MountLayout grants subPaths of the shared volume claims: workspaces and
// HOMEs on the NAS claim, Attachments and Reference Spaces on the read-only
// JuiceFS claim.
type MountLayout struct {
	WorkingClaim string
	InputsClaim  string
	// InputsRoot is where the persistent root lies within the inputs claim.
	InputsRoot string
}

var _ application.MountLayoutPort = MountLayout{}

func (m MountLayout) Working(directory string) application.MountGrant {
	return application.MountGrant{ClaimName: m.WorkingClaim, SubPath: directory}
}

func (m MountLayout) Attachments(owner string) application.MountGrant {
	return application.MountGrant{ClaimName: m.InputsClaim, SubPath: path.Join(m.InputsRoot, kindDirectory(owner, domain.FileKindAttachment)), ReadOnly: true}
}

func (m MountLayout) Reference(owner string) application.MountGrant {
	return application.MountGrant{ClaimName: m.InputsClaim, SubPath: path.Join(m.InputsRoot, referenceSpace(owner)), ReadOnly: true}
}
