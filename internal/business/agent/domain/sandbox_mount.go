package domain

// MountGrant is where Storage placed a Directory and its access mode, opaque
// to Agent: Agent only decides where it appears in the sandbox.
type MountGrant struct {
	ClaimName string
	SubPath   string
	ReadOnly  bool
}

// SandboxMounts are the Mount Grants of one start, one per located
// Directory in the order of its located layout's mounts.
type SandboxMounts []MountGrant
