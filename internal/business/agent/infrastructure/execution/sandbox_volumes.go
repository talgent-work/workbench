package execution

import (
	"fmt"
	"path"
	"strings"

	sdk "github.com/alibaba/OpenSandbox/sdks/sandbox/go"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// Where each granted space appears in the sandbox.
const (
	workspaceMountPath  = "/workspace"
	homeMountPath       = "/home/talgent"
	workInputsMountPath = "/workspace/inputs"
)

// sandboxVolumes places the grants Storage issued: the workspace and HOME
// read-write, the input source's Attachments read-only at /workspace/inputs
// and each read-only mount at /workspace/<name>, the name its caller gave. A Work
// without a Session Workspace writes to the sandbox's own /workspace, which
// ends with it. A grant is used exactly as issued; a missing or mis-scoped
// grant fails the start.
func sandboxVolumes(request domain.LaunchSpecification, mounts domain.SandboxMounts) ([]sdk.Volume, error) {
	if !validGrant(mounts.Home, false) {
		return nil, domain.ErrExecutionRequest
	}
	var volumes []sdk.Volume
	if request.WorkspaceReference != "" {
		if mounts.Workspace == nil || !validGrant(*mounts.Workspace, false) {
			return nil, domain.ErrExecutionRequest
		}
		volumes = append(volumes, volume("workspace", workspaceMountPath, *mounts.Workspace))
	}
	volumes = append(volumes, volume("home", homeMountPath, mounts.Home))
	if request.InputSource.Present() {
		if mounts.Inputs == nil || !validGrant(*mounts.Inputs, true) {
			return nil, domain.ErrExecutionRequest
		}
		volumes = append(volumes, volume("inputs", workInputsMountPath, *mounts.Inputs))
	}
	for i, mount := range request.ReadOnlyMounts {
		grant, granted := mounts.ReadOnly[mount.Name]
		if !granted || !validGrant(grant, true) {
			return nil, domain.ErrExecutionRequest
		}
		volumes = append(volumes, volume(fmt.Sprintf("read-only-%d", i), path.Join(workspaceMountPath, mount.Name), grant))
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
