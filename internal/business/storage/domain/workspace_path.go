package domain

import (
	"errors"
	"path"
	"strings"
)

var (
	// ErrInvalidWorkspacePath means a path leaves the workspace.
	ErrInvalidWorkspacePath = errors.New("storage: invalid workspace path")
	// ErrInvalidPublicationSource means a publication source is not one path
	// below the workspace outside inputs/.
	ErrInvalidPublicationSource = errors.New("storage: publication source must be one regular file under /workspace outside inputs/ and Reference Space mounts")
	// ErrSentContentWithWorkspace means a Work with a Session Workspace sent
	// content to publish; it publishes a file of that workspace instead.
	ErrSentContentWithWorkspace = errors.New("storage: a Work with a Session Workspace publishes a file of its workspace, not content it sends")
)

// MaxSentContentBytes bounds the content a Work without a Session Workspace
// sends to publish an Artifact.
const MaxSentContentBytes = 64 << 20

// InputsDirectory is where a workspace shows its input source's
// Attachments, read-only; nothing in it is the Work's own.
const InputsDirectory = "inputs"

// WorkspacePath is a clean path inside a Work's workspace, relative to
// /workspace; "." is its root.
type WorkspacePath string

// ParseWorkspaceDirectory reads a directory to browse; surrounding slashes
// and an empty path name the root.
func ParseWorkspaceDirectory(raw string) (WorkspacePath, error) {
	raw = strings.Trim(strings.TrimSpace(raw), "/")
	if raw == "" {
		return ".", nil
	}
	return parseRelative(raw, ErrInvalidWorkspacePath)
}

// ParsePublicationSource reads the workspace file a Work Agent publishes:
// exactly one clean path below the root, never the space's own Attachments.
// A Reference Space mount needs no rule here: on working storage its name is
// an empty mount point the sandbox can never write into, so nothing under it
// is found there.
func ParsePublicationSource(raw string) (WorkspacePath, error) {
	if strings.HasPrefix(raw, "/") {
		return "", ErrInvalidPublicationSource
	}
	source, err := parseRelative(raw, ErrInvalidPublicationSource)
	if err != nil || source == "." || source.UnderInputs() || source == InputsDirectory {
		return "", ErrInvalidPublicationSource
	}
	return source, nil
}

// ParseWorkspaceFile reads a workspace file a reader opens: one clean path
// below the root. Attachments under inputs/ are read as Attachments.
func ParseWorkspaceFile(raw string) (WorkspacePath, error) {
	file, err := parseRelative(strings.TrimPrefix(raw, "/"), ErrInvalidWorkspacePath)
	if err != nil || file == "." || file == InputsDirectory || file.UnderInputs() {
		return "", ErrInvalidWorkspacePath
	}
	return file, nil
}

func parseRelative(raw string, invalid error) (WorkspacePath, error) {
	if raw == "" || strings.ContainsAny(raw, "\\\x00") {
		return "", invalid
	}
	clean := path.Clean(raw)
	if clean != raw || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", invalid
	}
	return WorkspacePath(clean), nil
}

// UnderInputs reports whether the path lies inside inputs/.
func (p WorkspacePath) UnderInputs() bool {
	return strings.HasPrefix(string(p), InputsDirectory+"/")
}

// InMount finds the Reference Space mount the path is or lies inside, with
// its place in that space's current content, "." for the mount itself. A
// mount shows only the visible entries of its current content, so a hidden
// name at its root is refused.
func (p WorkspacePath) InMount(mounts []ReferenceMount) (ReferenceMount, string, bool, error) {
	for _, mount := range mounts {
		if string(p) != mount.Name && !strings.HasPrefix(string(p), mount.Name+"/") {
			continue
		}
		entry := strings.TrimPrefix(strings.TrimPrefix(string(p), mount.Name), "/")
		if entry == "" {
			return mount, ".", true, nil
		}
		if strings.HasPrefix(entry, ".") {
			return ReferenceMount{}, "", true, ErrInvalidWorkspacePath
		}
		return mount, entry, true, nil
	}
	return ReferenceMount{}, "", false, nil
}
