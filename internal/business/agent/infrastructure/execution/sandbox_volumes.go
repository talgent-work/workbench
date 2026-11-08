package execution

import (
	"fmt"
	"path"
	"strings"

	sdk "github.com/alibaba/OpenSandbox/sdks/sandbox/go"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// homeMountPath is the sandbox's HOME, where the layout's $HOME is mounted.
const homeMountPath = "/home/talgent"

// sandboxVolumes places the grants Storage issued for the Work's located
// layout, one per located Directory in layout order, each at its path with
// $HOME at the sandbox's HOME and in its access mode. A grant is used
// exactly as issued; a missing, surplus or mis-scoped grant fails the start.
func sandboxVolumes(request domain.LaunchSpecification, mounts domain.SandboxMounts) ([]sdk.Volume, error) {
	located := request.LocatedLayout.Mounts
	if len(located) != len(mounts) {
		return nil, domain.ErrExecutionRequest
	}
	volumes := make([]sdk.Volume, 0, len(located))
	for i, mount := range located {
		if !validGrant(mounts[i], mount.ReadOnly) {
			return nil, domain.ErrExecutionRequest
		}
		volumes = append(volumes, volume(fmt.Sprintf("directory-%d", i), domain.SandboxPath(mount.Path, homeMountPath), mounts[i]))
	}
	return volumes, nil
}

func validGrant(grant domain.MountGrant, readOnly bool) bool {
	sub := grant.SubPath
	return strings.TrimSpace(grant.ClaimName) != "" && grant.ReadOnly == readOnly && sub != "" && !path.IsAbs(sub) &&
		path.Clean(sub) == sub && sub != "." && sub != ".." && !strings.HasPrefix(sub, "../") && !strings.ContainsAny(sub, "\\\x00")
}

func volume(name, mountPath string, grant domain.MountGrant) sdk.Volume {
	no := false
	return sdk.Volume{Name: name, PVC: &sdk.PVC{ClaimName: grant.ClaimName, CreateIfNotExists: &no, DeleteOnSandboxTermination: &no}, MountPath: mountPath, SubPath: grant.SubPath, ReadOnly: grant.ReadOnly}
}
