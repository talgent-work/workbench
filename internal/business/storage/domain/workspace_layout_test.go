package domain

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func directoryID(t *testing.T, o DirectoryOwner, name string) DirectoryID {
	t.Helper()
	id, err := NewDirectoryID(o, name)
	require.NoError(t, err)
	return id
}

func layout(t *testing.T) WorkspaceLayout {
	t.Helper()
	workspace := directoryID(t, owner(t, "work", "w1"), "workspace")
	inputs := directoryID(t, owner(t, "subject", "case:1"), "inputs")
	nested := directoryID(t, owner(t, "project", "p1"), "knowledge")
	l, err := NewWorkspaceLayout(workspace, []MountedDirectory{{Path: "inputs", Directory: inputs}, {Path: "inputs/knowledge", Directory: nested}})
	require.NoError(t, err)
	return l
}

func TestWorkspacePaths(t *testing.T) {
	for raw, want := range map[string]WorkspacePath{"": ".", "/": ".", "/dist/": "dist", "a/b": "a/b"} {
		got, err := ParseWorkspaceDirectory(raw)
		require.NoError(t, err, raw)
		require.Equal(t, want, got)
	}
	for _, raw := range []string{"../up", "a/../b", "a//b", `a\b`} {
		_, err := ParseWorkspaceDirectory(raw)
		require.ErrorIs(t, err, ErrInvalidWorkspacePath, raw)
	}
	_, err := ParseWorkspaceFile("/")
	require.ErrorIs(t, err, ErrInvalidWorkspacePath, "the root is no file")
	file, err := ParseWorkspaceFile("/inputs/brief.md")
	require.NoError(t, err)
	require.Equal(t, WorkspacePath("inputs/brief.md"), file)
}

func TestALayoutPlacesEachPathInTheDirectoryItIsReadIn(t *testing.T) {
	l := layout(t)
	require.Equal(t, Place{Directory: l.Workspace, Path: "dist/a.md"}, l.Locate("dist/a.md"))
	require.Equal(t, Place{Directory: l.Workspace, Path: "."}, l.Locate("."))
	require.Equal(t, Place{Directory: l.Mounts[0].Directory, Path: ".", Mounted: true}, l.Locate("inputs"))
	require.Equal(t, Place{Directory: l.Mounts[0].Directory, Path: "brief.md", Mounted: true}, l.Locate("inputs/brief.md"))
	require.Equal(t, Place{Directory: l.Mounts[1].Directory, Path: "pages/a.md", Mounted: true}, l.Locate("inputs/knowledge/pages/a.md"), "the deepest mount holds the path")
	require.Equal(t, Place{Directory: l.Workspace, Path: "inputsx"}, l.Locate("inputsx"))
}

func TestALayoutHasOneMountPerPath(t *testing.T) {
	workspace := directoryID(t, owner(t, "work", "w1"), "workspace")
	inputs := directoryID(t, owner(t, "subject", "case:1"), "inputs")
	for name, mounts := range map[string][]MountedDirectory{
		"two at one path": {{Path: "inputs", Directory: inputs}, {Path: "inputs", Directory: workspace}},
		"at the root":     {{Path: ".", Directory: inputs}},
		"leaving it":      {{Path: "../inputs", Directory: inputs}},
		"no Directory":    {{Path: "inputs"}},
	} {
		_, err := NewWorkspaceLayout(workspace, mounts)
		require.ErrorIs(t, err, ErrInvalidWorkspaceLayout, name)
	}
	_, err := NewWorkspaceLayout(DirectoryID{}, nil)
	require.ErrorIs(t, err, ErrInvalidWorkspaceLayout)
}

func TestAPublicationSourceIsOneFileOutsideEveryMount(t *testing.T) {
	l := layout(t)
	source, err := l.PublicationSource("dist/report.md")
	require.NoError(t, err)
	require.Equal(t, WorkspacePath("dist/report.md"), source)
	for _, raw := range []string{"", ".", "/dist/report.md", "../x", "inputs", "inputs/brief.md", "inputs/knowledge/a.md"} {
		_, err := l.PublicationSource(raw)
		require.ErrorIs(t, err, ErrInvalidPublicationSource, raw)
	}
}

func TestFileNameIsOneDirectoryEntry(t *testing.T) {
	d := directoryID(t, owner(t, "subject", "case:1"), "inputs")
	for _, name := range []string{"", ".", "..", "a/b", `a\b`, " a", "a\x01"} {
		_, err := NewFilePosition(d, name)
		require.ErrorIs(t, err, ErrInvalidFileName, name)
	}
	position, err := NewFilePosition(d, "report.md")
	require.NoError(t, err)
	require.Equal(t, FilePosition{Directory: d, Name: "report.md"}, position)
	for _, raw := range []string{"", "/a", "a/../b", "..", "a\x00"} {
		_, err := ParseContentPath(raw)
		require.ErrorIs(t, err, ErrInvalidContentPath, raw)
	}
}
