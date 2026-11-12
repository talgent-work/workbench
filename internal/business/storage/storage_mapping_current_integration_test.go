//go:build integration

package storage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/storage/application"
	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

// viewNames lists the files a mapped path shows, by their path in it.
func (f *fixture) viewNames(path string) []string {
	f.t.Helper()
	var names []string
	root := f.viewOf(path)
	require.NoError(f.t, filepath.WalkDir(root, func(name string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		relative, err := filepath.Rel(root, name)
		names = append(names, filepath.ToSlash(relative))
		return err
	}))
	return names
}

// requireShowsDirectory asserts the mapped path shows exactly the
// Directory's files, each the same file.
func (f *fixture) requireShowsDirectory(located application.Located, path string, names ...string) {
	f.t.Helper()
	require.ElementsMatch(f.t, names, f.viewNames(path))
	for _, name := range names {
		original, err := os.Stat(filepath.Join(f.folder(located), name))
		require.NoError(f.t, err)
		shown, err := os.Lstat(filepath.Join(f.viewOf(path), name))
		require.NoError(f.t, err)
		require.True(f.t, os.SameFile(original, shown), name)
	}
}

// Every create, replacement, rename and deletion Storage carries out in a
// mapped Directory reaches its mapped paths.
func TestFileChangesReachTheMappedPaths(t *testing.T) {
	f := newFixture(t)
	inputs := fileSpace(named("inputs", "subject", "case:1"))
	token := f.open(inputs)
	require.Equal(t, 200, f.upload(inputs.Directory, token, "a.md", "a", false).StatusCode)
	require.NoError(t, f.mapAt(inputs, "view/p1/case-1/inputs"))
	require.NoError(t, f.mapAt(inputs, "view/p2/case-1"), "a second mapping of the same Directory")
	both := func(names ...string) {
		t.Helper()
		f.requireShowsDirectory(inputs, "view/p1/case-1/inputs", names...)
		f.requireShowsDirectory(inputs, "view/p2/case-1", names...)
	}
	both("a.md")

	require.Equal(t, 200, f.upload(inputs.Directory, token, "b.md", "b", false).StatusCode)
	both("a.md", "b.md")
	require.Equal(t, 200, f.upload(inputs.Directory, token, "a.md", "a, revised", true).StatusCode)
	both("a.md", "b.md")
	content, err := os.ReadFile(filepath.Join(f.viewOf("view/p1/case-1/inputs"), "a.md"))
	require.NoError(t, err)
	require.Equal(t, "a, revised", string(content))

	_, err = f.app.RenameFile(t.Context(), at(inputs.Directory, "b.md"), token, "a.md", domain.Actor{UserID: "user-1"})
	require.ErrorIs(t, err, application.ErrNameTaken)
	both("a.md", "b.md")
	_, err = f.app.RenameFile(t.Context(), at(inputs.Directory, "b.md"), token, "c.md", domain.Actor{UserID: "user-1"})
	require.NoError(t, err)
	both("a.md", "c.md")
	require.NoError(t, f.app.DeleteFile(t.Context(), at(inputs.Directory, "a.md"), token, domain.Actor{UserID: "user-1"}))
	both("c.md")

	artifact, err := f.app.PublishArtifact(t.Context(), application.ArtifactSource{SpaceToken: token, WorkID: "work-1", Content: []byte("report")}, "report.md", false)
	require.NoError(t, err)
	require.Equal(t, "report.md", artifact.Position.Name)
	both("c.md", "report.md")
}

// Each whole-content replacement of a mapped Directory reaches its mapped
// path: the current content's files, and none of an earlier content.
func TestWholeContentReplacementReachesTheMappedPath(t *testing.T) {
	f := newFixture(t)
	knowledge := fileSpace(named("knowledge", "project", "p1"))
	token := f.open(knowledge)
	require.NoError(t, f.app.ReplaceContent(t.Context(), knowledge.Directory, token, []application.ContentFile{
		{Path: "a.md", Content: []byte("a")}, {Path: "sub/b.md", Content: []byte("b")}, {Path: ".hidden/state", Content: []byte("h")},
	}))
	require.NoError(t, f.mapAt(knowledge, "view/p1/knowledge"))
	require.ElementsMatch(t, []string{"a.md", "sub/b.md"}, f.viewNames("view/p1/knowledge"), "hidden top-level entries stay out")

	require.NoError(t, f.app.ReplaceContent(t.Context(), knowledge.Directory, token, []application.ContentFile{{Path: "c/d.md", Content: []byte("d")}}))
	require.Equal(t, []string{"c/d.md"}, f.viewNames("view/p1/knowledge"))
	content, err := os.ReadFile(filepath.Join(f.viewOf("view/p1/knowledge"), "c", "d.md"))
	require.NoError(t, err)
	require.Equal(t, "d", string(content))
	require.NoDirExists(t, filepath.Join(f.viewOf("view/p1/knowledge"), "sub"), "an emptied folder goes")
}

// blockView puts a non-empty folder where a mapped path's link of name
// goes, so Storage can neither remove nor make that link, as an interrupted
// change would leave it.
func (f *fixture) blockView(path, name string) func() {
	f.t.Helper()
	blocker := filepath.Join(f.viewOf(path), name)
	require.NoError(f.t, os.RemoveAll(blocker))
	require.NoError(f.t, os.MkdirAll(filepath.Join(blocker, "held"), 0o755))
	return func() { require.NoError(f.t, os.RemoveAll(blocker)) }
}

// A change that cannot reach a mapped path leaves the Mapped View missing a
// file, never showing one the Directory no longer holds: a link that goes
// is removed before the Directory changes, one that comes is made after.
func TestAnInterruptedChangeLeavesTheViewWithFewerFilesNeverMore(t *testing.T) {
	f := newFixture(t)
	inputs := fileSpace(named("inputs", "subject", "case:2"))
	token := f.open(inputs)
	require.Equal(t, 200, f.upload(inputs.Directory, token, "kept.md", "kept", false).StatusCode)
	require.NoError(t, f.mapAt(inputs, "view/p1/case-2"))

	// A link that cannot come: the create is carried out, the view lacks it.
	unblock := f.blockView("view/p1/case-2", "new.md")
	require.Equal(t, 200, f.upload(inputs.Directory, token, "new.md", "new", false).StatusCode)
	require.FileExists(t, filepath.Join(f.folder(inputs), "new.md"))
	unblock()
	f.requireShowsDirectory(inputs, "view/p1/case-2", "kept.md")
	require.NoError(t, f.mapAt(inputs, "view/p1/case-2"), "mapping again aligns the view")
	f.requireShowsDirectory(inputs, "view/p1/case-2", "kept.md", "new.md")

	// A link that cannot go: the deletion, replacement and rename are refused
	// before the Directory changes, so no file stays shown that it lacks.
	unblock = f.blockView("view/p1/case-2", "kept.md")
	require.Error(t, f.app.DeleteFile(t.Context(), at(inputs.Directory, "kept.md"), token, domain.Actor{UserID: "user-1"}))
	require.NotEqual(t, 200, f.upload(inputs.Directory, token, "kept.md", "changed", true).StatusCode)
	_, err := f.app.RenameFile(t.Context(), at(inputs.Directory, "kept.md"), token, "moved.md", domain.Actor{UserID: "user-1"})
	require.Error(t, err)
	content, err := os.ReadFile(filepath.Join(f.folder(inputs), "kept.md"))
	require.NoError(t, err)
	require.Equal(t, "kept", string(content), "the Directory is unchanged")
	unblock()
}

// Reclaiming a mapped Directory leaves nothing of it in the Mapped View.
func TestReclamationLeavesNothingInTheMappedView(t *testing.T) {
	f := newFixture(t)
	outputs := application.Located{Directory: named("outputs", "work", "work-7"), Backend: domain.BackendJuiceFS, Lifetime: domain.LifetimeWork}
	token := f.open(outputs)
	_, err := f.app.PublishArtifact(t.Context(), application.ArtifactSource{SpaceToken: token, WorkID: "work-7", Content: []byte("out")}, "out.md", false)
	require.NoError(t, err)
	require.NoError(t, f.mapAt(outputs, "view/w/work-7/outputs"))
	require.NoError(t, f.mapAt(fileSpace(named("inputs", "subject", "case:3")), "view/w/case-3"))
	f.requireShowsDirectory(outputs, "view/w/work-7/outputs", "out.md")

	require.NoError(t, f.app.RequestWorkReclamation(t.Context(), "work-7"))
	require.NoDirExists(t, f.viewOf("view/w/work-7"), "its files and emptied folders are gone")
	require.DirExists(t, f.viewOf("view/w/case-3"), "another mapping stays")
	f.removals()
	require.NoDirExists(t, f.folder(outputs))
	require.NoError(t, f.app.RequestWorkReclamation(t.Context(), "work-7"), "a repeated reclamation changes nothing")
}
