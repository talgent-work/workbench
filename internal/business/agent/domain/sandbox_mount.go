package domain

// MountGrant is where Storage placed a space and its access mode, opaque to
// Agent: Agent only decides where it appears in the sandbox.
type MountGrant struct {
	ClaimName string
	SubPath   string
	ReadOnly  bool
}

// SandboxMounts are the Mount Grants of one start: the HOME read-write, the
// Session Workspace read-write when the Work has one, the input source's
// Attachments read-only when the Work has an input source, and each
// read-only mount its caller supplied, by the name it gave.
type SandboxMounts struct {
	Home      MountGrant
	Workspace *MountGrant
	Inputs    *MountGrant
	ReadOnly  map[string]MountGrant
}

// SandboxMountRequest names a start's spaces by business identity.
type SandboxMountRequest struct {
	HomeOwner          string
	WorkID             string
	WorkspaceReference string
	InputSource        WorkInputSource
	ReadOnlyMounts     []ReadOnlyMount
}

// MountRequest names the spaces of this start: the definition's HOME owner,
// this Work's Session Workspace, its input source and its read-only mounts.
func (r StartExecutionRequest) MountRequest() SandboxMountRequest {
	return SandboxMountRequest{HomeOwner: r.HomeBinding.OwnerID, WorkID: r.WorkID, WorkspaceReference: r.WorkspaceReference, InputSource: r.InputSource, ReadOnlyMounts: r.ReadOnlyMounts}
}
