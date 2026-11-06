package domain

import (
	"errors"
	"testing"
)

// A publication source is one clean path below the workspace, never the
// space's Attachments; browsing reaches any directory inside it.
func TestWorkspacePaths(t *testing.T) {
	for _, valid := range []string{"report.md", "dist/report.pdf", ".hidden/file", "handbook.md", "handbookbase/a.md"} {
		if got, err := ParsePublicationSource(valid); err != nil || string(got) != valid {
			t.Fatalf("source %q: %q, %v", valid, got, err)
		}
	}
	for _, invalid := range []string{"", ".", "/etc/passwd", "../x", "a/../b", "dist/", "inputs", "inputs/brief.md", `a\b`} {
		if _, err := ParsePublicationSource(invalid); !errors.Is(err, ErrInvalidPublicationSource) {
			t.Fatalf("source %q accepted", invalid)
		}
	}
	for raw, want := range map[string]WorkspacePath{"": ".", "/": ".", "/dist/": "dist", "inputs": "inputs"} {
		if got, err := ParseWorkspaceDirectory(raw); err != nil || got != want {
			t.Fatalf("directory %q: %q, %v", raw, got, err)
		}
	}
	if _, err := ParseWorkspaceDirectory("../home"); !errors.Is(err, ErrInvalidWorkspacePath) {
		t.Fatalf("escape accepted: %v", err)
	}
}

// A mount shows the visible entries of its Reference Space's current content.
func TestMountEntries(t *testing.T) {
	mounts := []ReferenceMount{{Name: "handbook", Owner: "p-1"}, {Name: "guides", Owner: "p-2"}}
	for raw, want := range map[WorkspacePath][2]string{
		"handbook": {"handbook", "."}, "handbook/index.md": {"handbook", "index.md"}, "guides/pages/a.md": {"guides", "pages/a.md"}, "handbook/pages/.draft": {"handbook", "pages/.draft"},
	} {
		mount, entry, found, err := raw.InMount(mounts)
		if err != nil || !found || mount.Name != want[0] || entry != want[1] {
			t.Fatalf("entry %q: %q %q %v %v", raw, mount.Name, entry, found, err)
		}
	}
	for _, refused := range []WorkspacePath{"handbook/.current", "handbook/.contents/v1/index.md"} {
		if _, _, found, err := refused.InMount(mounts); !found || !errors.Is(err, ErrInvalidWorkspacePath) {
			t.Fatalf("entry %q accepted", refused)
		}
	}
	for _, outside := range []WorkspacePath{"handbookbase", "notes/handbook", "inputs"} {
		if _, _, found, _ := outside.InMount(mounts); found {
			t.Fatalf("entry %q found in a mount", outside)
		}
	}
}
