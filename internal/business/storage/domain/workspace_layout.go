package domain

import (
	"errors"
	"path"
	"strings"
)

var (
	// ErrInvalidWorkspacePath means a path leaves the workspace.
	ErrInvalidWorkspacePath = errors.New("storage: invalid workspace path")
	// ErrInvalidWorkspaceLayout means a layout names no Work Directory, or a
	// mount not at one clean path of its own.
	ErrInvalidWorkspaceLayout = errors.New("storage: invalid workspace layout")
	// ErrInvalidPublicationSource means a publication source is not one path
	// below the Work Directory outside every Directory mounted in it.
	ErrInvalidPublicationSource = errors.New("storage: publication source must be one regular file of the Session Workspace outside every Directory mounted in it")
	// ErrSentContentWithWorkspace means a Work with a Session Workspace sent
	// content to publish; it publishes a file of that workspace instead.
	ErrSentContentWithWorkspace = errors.New("storage: a Work with a Session Workspace publishes a file of its workspace, not content it sends")
)

// MaxSentContentBytes bounds the content a Work without a Session Workspace
// sends to publish an Artifact.
const MaxSentContentBytes = 64 << 20

// WorkspacePath is a clean path inside a Work Directory; "." is its root.
type WorkspacePath string

// ParseWorkspaceDirectory reads a directory to browse; surrounding slashes
// and an empty path name the root.
func ParseWorkspaceDirectory(raw string) (WorkspacePath, error) {
	raw = strings.Trim(strings.TrimSpace(raw), "/")
	if raw == "" {
		return ".", nil
	}
	return parseRelative(raw)
}

// ParseWorkspaceFile reads a workspace file a reader opens: one clean path
// below the root.
func ParseWorkspaceFile(raw string) (WorkspacePath, error) {
	file, err := parseRelative(strings.TrimPrefix(raw, "/"))
	if err != nil || file == "." {
		return "", ErrInvalidWorkspacePath
	}
	return file, nil
}

func parseRelative(raw string) (WorkspacePath, error) {
	if raw == "" || strings.ContainsAny(raw, "\\\x00") {
		return "", ErrInvalidWorkspacePath
	}
	clean := path.Clean(raw)
	if clean != raw || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", ErrInvalidWorkspacePath
	}
	return WorkspacePath(clean), nil
}

// within reports whether the path is mount or lies inside it, with its place
// inside, "." for mount itself.
func (p WorkspacePath) within(mount WorkspacePath) (string, bool) {
	if p == mount {
		return ".", true
	}
	inner, ok := strings.CutPrefix(string(p), string(mount)+"/")
	return inner, ok
}

// MountedDirectory is a Directory mounted inside a Work Directory at Path.
type MountedDirectory struct {
	Path      WorkspacePath
	Directory DirectoryID
}

// WorkspaceLayout is a Work Directory as its sandbox sees it: the Directory
// and the Directories mounted inside it, each at its own path, as Agent
// placed them.
type WorkspaceLayout struct {
	Workspace DirectoryID
	Mounts    []MountedDirectory
}

// NewWorkspaceLayout admits a layout whose every mount is at its own clean
// path below the Work Directory's root.
func NewWorkspaceLayout(workspace DirectoryID, mounts []MountedDirectory) (WorkspaceLayout, error) {
	if workspace.Owner.IsZero() {
		return WorkspaceLayout{}, ErrInvalidWorkspaceLayout
	}
	seen := make(map[WorkspacePath]bool, len(mounts))
	for _, mount := range mounts {
		if mount.Directory.Owner.IsZero() || !cleanRelative(string(mount.Path)) || seen[mount.Path] {
			return WorkspaceLayout{}, ErrInvalidWorkspaceLayout
		}
		seen[mount.Path] = true
	}
	return WorkspaceLayout{Workspace: workspace, Mounts: append([]MountedDirectory(nil), mounts...)}, nil
}

// Place is where a workspace path is read: in the Work Directory itself, or
// in the Directory mounted deepest around it, at Path inside that Directory.
type Place struct {
	Directory DirectoryID
	// Path is relative to Directory's root; "." is the root.
	Path string
	// Mounted says Directory is mounted in the Work Directory.
	Mounted bool
}

// Locate finds where a sandbox reads the path: the deepest mount it is or
// lies inside, otherwise the Work Directory.
func (l WorkspaceLayout) Locate(p WorkspacePath) Place {
	place := Place{Directory: l.Workspace, Path: string(p)}
	depth := -1
	for _, mount := range l.Mounts {
		inner, inside := p.within(mount.Path)
		if inside && strings.Count(string(mount.Path), "/") > depth {
			depth = strings.Count(string(mount.Path), "/")
			place = Place{Directory: mount.Directory, Path: inner, Mounted: true}
		}
	}
	return place
}

// PublicationSource reads the workspace file a Work Agent publishes: one
// clean path below the root, outside every Directory mounted in the Work
// Directory.
func (l WorkspaceLayout) PublicationSource(raw string) (WorkspacePath, error) {
	if strings.HasPrefix(raw, "/") {
		return "", ErrInvalidPublicationSource
	}
	source, err := parseRelative(raw)
	if err != nil || source == "." || l.Locate(source).Mounted {
		return "", ErrInvalidPublicationSource
	}
	return source, nil
}
