//go:build integration

package storage

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/storage/application"
	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

func entryNames(listing application.WorkspaceListing) map[string]application.WorkspaceEntry {
	byName := map[string]application.WorkspaceEntry{}
	for _, entry := range listing.Entries {
		byName[entry.Name] = entry
	}
	return byName
}

// A Workspace View shows each Directory mounted in the Work Directory where
// it is mounted; a file Storage wrote there carries its File position.
func TestWorkspaceViewShowsMountedDirectoriesWhereTheyAreMounted(t *testing.T) {
	f := newFixture(t)
	f.upload(caseInputs, f.open(fileSpace(caseInputs)), "brief.md", "b", false)
	require.NoError(t, os.WriteFile(filepath.Join(f.folder(fileSpace(caseInputs)), "harness.md"), []byte("h"), 0o644))
	unopened := named("outputs", "subject", "case:1")
	layout := f.workspace("work-1", map[string]application.Located{"inputs": fileSpace(caseInputs), "deep/outputs": fileSpace(unopened)})
	layout.Mounts = append(layout.Mounts, application.Mount{Path: "never", Directory: named("never", "subject", "case:1")})
	f.workspaceFile("work-1", "notes.md", "n")

	root, err := f.app.WorkspaceView(t.Context(), layout, ".")
	require.NoError(t, err)
	entries := entryNames(root)
	require.Equal(t, application.WorkspaceDirectory, entries["inputs"].Kind)
	require.Equal(t, application.WorkspaceFile, entries["notes.md"].Kind)
	require.Nil(t, entries["notes.md"].Position, "a Harness file has no File position")

	inputs, err := f.app.WorkspaceView(t.Context(), layout, "inputs")
	require.NoError(t, err)
	entries = entryNames(inputs)
	require.Len(t, entries, 2)
	require.NotNil(t, entries["brief.md"].Position)
	require.Equal(t, "brief.md", entries["brief.md"].Position.Name)
	require.Equal(t, "inputs", entries["brief.md"].Position.Directory.Name)
	require.Nil(t, entries["harness.md"].Position)

	empty, err := f.app.WorkspaceView(t.Context(), layout, "never")
	require.NoError(t, err)
	require.Empty(t, empty.Entries, "a mounted Directory never opened has no files")
	_, err = f.app.WorkspaceView(t.Context(), layout, "../escape")
	require.ErrorIs(t, err, domain.ErrInvalidWorkspacePath)
}

func TestWorkspaceViewListsTheFirstEntriesByName(t *testing.T) {
	f := newFixtureWith(t, func(config *Config) { config.WorkspaceListEntries = 2 })
	layout := f.workspace("work-1", nil)
	for _, name := range []string{"c.md", "a.md", "b.md"} {
		f.workspaceFile("work-1", name, name)
	}
	listing, err := f.app.WorkspaceView(t.Context(), layout, ".")
	require.NoError(t, err)
	require.Equal(t, 3, listing.Total)
	require.Equal(t, "a.md", listing.Entries[0].Name)
	require.Equal(t, "b.md", listing.Entries[1].Name)
	require.Len(t, listing.Entries, 2)
}

// A workspace file link reads the file as it is; a preview link refuses a
// file changed since its preview was decided; no symbolic link is opened.
func TestWorkspaceFileLinks(t *testing.T) {
	f := newFixture(t)
	layout := f.workspace("work-1", nil)
	f.workspaceFile("work-1", "notes.txt", "first")
	link, err := f.app.IssueWorkspaceFileLink(t.Context(), layout, "notes.txt")
	require.NoError(t, err)
	status, body, _ := f.get(link.URL)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "first", body)

	preview, err := f.app.PreviewWorkspaceFile(t.Context(), layout, "notes.txt")
	require.NoError(t, err)
	require.NotNil(t, preview.Link)
	f.workspaceFile("work-1", "notes.txt", "second, longer")
	status, _, _ = f.get(preview.Link.URL)
	require.Equal(t, http.StatusConflict, status)
	_, body, _ = f.get(link.URL)
	require.Equal(t, "second, longer", body, "a download link reads the file as it is now")

	workspace := f.folder(workDirectory("work-1", "workspace"))
	require.NoError(t, os.Symlink("notes.txt", filepath.Join(workspace, "alias.txt")))
	_, err = f.app.IssueWorkspaceFileLink(t.Context(), layout, "alias.txt")
	require.Error(t, err)
	_, err = f.app.IssueWorkspaceFileLink(t.Context(), layout, "absent.txt")
	require.ErrorIs(t, err, application.ErrFileNotFound)
}

// A Directory replaced as a whole shows only its current content, to its
// readers and to a sandbox that mounted it before the replacement.
func TestWholeContentReplacementUnderAStandingMount(t *testing.T) {
	f := newFixture(t)
	knowledge := named("knowledge", "subject", "case:1")
	files, err := f.app.ReadContent(t.Context(), knowledge, nil)
	require.NoError(t, err)
	require.Empty(t, files, "a Directory never opened has no content")
	token := f.open(fileSpace(knowledge))
	layout := f.workspace("work-1", map[string]application.Located{"knowledge": fileSpace(knowledge)})
	folder := f.folder(fileSpace(knowledge))

	require.ErrorIs(t, f.app.ReplaceContent(t.Context(), knowledge, "forged", nil), domain.ErrSpaceTokenRejected)
	require.ErrorIs(t, f.app.ReplaceContent(t.Context(), knowledge, token, []application.ContentFile{{Path: "../x", Content: []byte("x")}}), domain.ErrInvalidContentPath)
	first := []application.ContentFile{{Path: "a.md", Content: []byte("a")}, {Path: "sub/b.md", Content: []byte("b")}}
	require.NoError(t, f.app.ReplaceContent(t.Context(), knowledge, token, first))
	read, err := f.app.ReadContent(t.Context(), knowledge, nil)
	require.NoError(t, err)
	require.ElementsMatch(t, first, read)
	view, err := f.app.WorkspaceView(t.Context(), layout, "knowledge")
	require.NoError(t, err)
	require.Equal(t, []string{"a.md", "sub"}, []string{view.Entries[0].Name, view.Entries[1].Name})
	require.Len(t, view.Entries, 2, "nothing Storage keeps for the switch is visible")
	mounted, err := os.ReadFile(filepath.Join(folder, "sub", "b.md"))
	require.NoError(t, err)
	require.Equal(t, "b", string(mounted))

	require.NoError(t, f.app.ReplaceContent(t.Context(), knowledge, token, []application.ContentFile{{Path: "c.md", Content: []byte("c")}}))
	read, err = f.app.ReadContent(t.Context(), knowledge, []string{"c.md"})
	require.NoError(t, err)
	require.Equal(t, []application.ContentFile{{Path: "c.md", Content: []byte("c")}}, read)
	view, err = f.app.WorkspaceView(t.Context(), layout, "knowledge")
	require.NoError(t, err)
	require.Len(t, view.Entries, 1)
	require.Equal(t, "c.md", view.Entries[0].Name)
	require.NoFileExists(t, filepath.Join(folder, "a.md"), "earlier content is gone for a standing mount")
	mounted, err = os.ReadFile(filepath.Join(folder, "c.md"))
	require.NoError(t, err)
	require.Equal(t, "c", string(mounted))
	_, err = f.app.IssueWorkspaceFileLink(t.Context(), layout, "knowledge/a.md")
	require.ErrorIs(t, err, application.ErrFileNotFound)
}

// A rename whose record fails is moved back, so its Share Links keep
// reaching the file; links left by an unrecorded deletion never reach a
// later file at that name.
func TestInterruptedFileOperationsLeaveNoLinkReachingTheWrongFile(t *testing.T) {
	f := newFixture(t)
	token := f.open(fileSpace(caseInputs))
	f.upload(caseInputs, token, "a.md", "a", false)
	share, err := f.app.CreateShareLink(t.Context(), at(caseInputs, "a.md"), "user-1")
	require.NoError(t, err)

	f.directories.failing.Store(true)
	_, err = f.app.RenameFile(t.Context(), at(caseInputs, "a.md"), token, "b.md", "user-1")
	require.ErrorIs(t, err, errInterrupted)
	require.ErrorIs(t, f.app.DeleteFile(t.Context(), at(caseInputs, "a.md"), token, "user-1"), errInterrupted)
	f.directories.failing.Store(false)
	require.NotContains(t, f.files(caseInputs), "b.md", "the unrecorded rename was moved back")
	require.NotContains(t, f.files(caseInputs), "a.md", "the file is gone though its deletion was not recorded")

	require.Equal(t, http.StatusOK, f.upload(caseInputs, token, "a.md", "someone else's", false).StatusCode)
	status, _, _ := f.get(share.URL)
	require.Equal(t, http.StatusGone, status, "a stale link does not reach the later file")
	require.Empty(t, f.files(caseInputs)["a.md"].ShareLinks)

	// The interrupted deletion published once; the rename published nothing.
	require.Eventually(t, func() bool { return len(f.published.all()) == 3 }, 5*time.Second, 10*time.Millisecond)
}
