package domain

import (
	"errors"
	"path"
	"strings"
)

// ErrInvalidReferencePath means a Reference Space file path is not a clean
// relative path within its content.
var ErrInvalidReferencePath = errors.New("storage: invalid reference file path")

// ReferencePath is a clean relative path of a file within a Reference
// Space's content.
type ReferencePath string

// ParseReferencePath admits a clean relative path, so a file never escapes
// its content. A hidden name is the writer's own: a mounted space shows only
// the visible entries of its current content.
func ParseReferencePath(raw string) (ReferencePath, error) {
	if raw == "" || strings.ContainsAny(raw, "\\\x00") || path.IsAbs(raw) || path.Clean(raw) != raw || raw == "." || raw == ".." || strings.HasPrefix(raw, "../") {
		return "", ErrInvalidReferencePath
	}
	return ReferencePath(raw), nil
}

// ReferenceMount is a Reference Space mounted read-only in a workspace at
// the name its caller gave.
type ReferenceMount struct {
	Name  string
	Owner string
}

// NewReferenceMount admits a mount at one visible directory of the workspace
// root other than inputs/, which shows the input source's Attachments.
func NewReferenceMount(name, owner string) (ReferenceMount, error) {
	owner, err := ParseOwner(owner)
	if err != nil {
		return ReferenceMount{}, err
	}
	if name != strings.TrimSpace(name) || name == InputsDirectory || strings.HasPrefix(name, ".") || ValidateFileName(name) != nil {
		return ReferenceMount{}, ErrInvalidReferenceMount
	}
	return ReferenceMount{Name: name, Owner: owner}, nil
}

// ErrInvalidReferenceMount means a mount name is not one visible directory
// of the workspace root, is inputs, or repeats another mount's name.
var ErrInvalidReferenceMount = errors.New("storage: invalid reference mount")

// NewReferenceMounts admits a Work's mounts, each at its own name.
func NewReferenceMounts(raw []ReferenceMount) ([]ReferenceMount, error) {
	mounts := make([]ReferenceMount, 0, len(raw))
	seen := map[string]bool{}
	for _, entry := range raw {
		mount, err := NewReferenceMount(entry.Name, entry.Owner)
		if err != nil {
			return nil, err
		}
		if seen[mount.Name] {
			return nil, ErrInvalidReferenceMount
		}
		seen[mount.Name] = true
		mounts = append(mounts, mount)
	}
	return mounts, nil
}
