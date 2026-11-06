package domain

import (
	"fmt"
	"strings"
)

// ReadOnlyMount is an opaque space reference the creating caller supplies
// and the name it appears under in the Work's sandbox, beside the input
// source. The Work retains its mounts from creation unchanged and hands each
// reference to Storage for every start, never interpreting it.
type ReadOnlyMount struct {
	Name      string
	Reference string
}

const (
	maxReadOnlyMounts         = 8
	maxReadOnlyMountName      = 64
	maxReadOnlyMountReference = 255
	// inputsMountName is where the input source's Attachments appear.
	inputsMountName = "inputs"
)

// ValidateReadOnlyMounts admits mounts each at its own visible directory of
// the workspace, none at the input source's.
func ValidateReadOnlyMounts(mounts []ReadOnlyMount) error {
	if len(mounts) > maxReadOnlyMounts {
		return fmt.Errorf("%w: too many read-only mounts", ErrInvalidWork)
	}
	seen := make(map[string]bool, len(mounts))
	for _, mount := range mounts {
		name, reference := mount.Name, mount.Reference
		if name == "" || len(name) > maxReadOnlyMountName || strings.TrimSpace(name) != name || strings.HasPrefix(name, ".") ||
			strings.ContainsAny(name, "/\\\x00") || name == inputsMountName || seen[name] {
			return fmt.Errorf("%w: invalid read-only mount name", ErrInvalidWork)
		}
		if reference == "" || len(reference) > maxReadOnlyMountReference || strings.TrimSpace(reference) != reference || strings.ContainsRune(reference, 0) {
			return fmt.Errorf("%w: invalid read-only mount reference", ErrInvalidWork)
		}
		seen[name] = true
	}
	return nil
}
