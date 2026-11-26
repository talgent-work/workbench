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
	content, err := f.app.ReadContent(t.Context(), knowledge, nil)
	require.NoError(t, err)
	require.Empty(t, content.Files, "a Directory never opened has no content")
	require.Empty(t, content.Revision)
	token := f.open(fileSpace(knowledge))
	layout := f.workspace("work-1", map[string]application.Located{"knowledge": fileSpace(knowledge)})
	folder := f.folder(fileSpace(knowledge))

	require.ErrorIs(t, f.replace(knowledge, "forged", nil), domain.ErrSpaceTokenRejected)
	require.ErrorIs(t, f.replace(knowledge, token, []application.ContentFile{{Path: "../x", Content: []byte("x")}}), domain.ErrInvalidContentPath)
	first := []application.ContentFile{{Path: "a.md", Content: []byte("a")}, {Path: "sub/b.md", Content: []byte("b")}}
	require.NoError(t, f.replace(knowledge, token, first))
	read, err := f.app.ReadContent(t.Context(), knowledge, nil)
	require.NoError(t, err)
	require.ElementsMatch(t, first, read.Files)
	view, err := f.app.WorkspaceView(t.Context(), layout, "knowledge")
	require.NoError(t, err)
	require.Equal(t, []string{"a.md", "sub"}, []string{view.Entries[0].Name, view.Entries[1].Name})
	require.Len(t, view.Entries, 2, "nothing Storage keeps for the switch is visible")
	mounted, err := os.ReadFile(filepath.Join(folder, "sub", "b.md"))
	require.NoError(t, err)
	require.Equal(t, "b", string(mounted))

	require.NoError(t, f.replace(knowledge, token, []application.ContentFile{{Path: "c.md", Content: []byte("c")}}))
	read, err = f.app.ReadContent(t.Context(), knowledge, []string{"c.md"})
	require.NoError(t, err)
	require.Equal(t, []application.ContentFile{{Path: "c.md", Content: []byte("c")}}, read.Files)
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

// A replacement that names the content it expects switches in only while
// that content is current: a writer that made its content from an earlier
// read never switches over a content it did not read.
func TestContentReplacementExpectingAContentSwitchesOnlyOverIt(t *testing.T) {
	f := newFixture(t)
	knowledge := named("knowledge", "subject", "case:2")
	token := f.open(fileSpace(knowledge))
	a := []application.ContentFile{{Path: "a.md", Content: []byte("a")}}
	b := []application.ContentFile{{Path: "b.md", Content: []byte("b")}}

	first, err := f.app.ReplaceContent(t.Context(), knowledge, token, domain.NoContent(), a)
	require.NoError(t, err)
	require.NotEmpty(t, first)
	_, err = f.app.ReplaceContent(t.Context(), knowledge, token, domain.NoContent(), b)
	require.ErrorIs(t, err, domain.ErrContentChanged, "a content is already current")
	read, err := f.app.ReadContent(t.Context(), knowledge, nil)
	require.NoError(t, err)
	require.Equal(t, first, read.Revision)
	require.Equal(t, a, read.Files)

	expected, err := domain.ExpectRevision(read.Revision)
	require.NoError(t, err)
	second, err := f.app.ReplaceContent(t.Context(), knowledge, token, expected, b)
	require.NoError(t, err)
	require.NotEqual(t, first, second, "each switch takes a fresh revision")

	_, err = f.app.ReplaceContent(t.Context(), knowledge, token, expected, a)
	require.ErrorIs(t, err, domain.ErrContentChanged, "the read content was replaced since")
	read, err = f.app.ReadContent(t.Context(), knowledge, nil)
	require.NoError(t, err)
	require.Equal(t, second, read.Revision)
	require.Equal(t, b, read.Files, "a refused replacement changes nothing")
}

// A Share Link reaches its file by File ID, so renames and deletions record
// nothing: they complete while the Directory's record cannot change, and a
// later file at a freed name is never reached through an earlier link.
func TestRenamesAndDeletionsLeaveNoLinkReachingTheWrongFile(t *testing.T) {
	f := newFixture(t)
	token := f.open(fileSpace(caseInputs))
	f.upload(caseInputs, token, "a.md", "a", false)
	share, err := f.app.CreateShareLink(t.Context(), at(caseInputs, "a.md"), domain.Actor{UserID: "user-1"})
	require.NoError(t, err)

	f.directories.failing.Store(true)
	_, err = f.app.RenameFile(t.Context(), at(caseInputs, "a.md"), token, "b.md", domain.Actor{UserID: "user-1"})
	require.NoError(t, err, "a rename records nothing")
	require.Equal(t, http.StatusOK, f.upload(caseInputs, token, "a.md", "someone else's", false).StatusCode)
	status, body, _ := f.get(share.URL)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "a", body, "the link follows the renamed file, not the later one at its old name")
	require.NoError(t, f.app.DeleteFile(t.Context(), at(caseInputs, "b.md"), token, domain.Actor{UserID: "user-1"}), "a deletion records nothing")
	f.directories.failing.Store(false)

	require.Equal(t, http.StatusOK, f.upload(caseInputs, token, "b.md", "another", false).StatusCode)
	status, _, _ = f.get(share.URL)
	require.Equal(t, http.StatusNotFound, status, "a link to a deleted file answers that it is gone, never reaching a later file at its name")
	require.Empty(t, f.files(caseInputs)["a.md"].ShareLinks)
	require.Empty(t, f.files(caseInputs)["b.md"].ShareLinks)

	// Two uploads, the rename, the deletion and the last upload published.
	require.Eventually(t, func() bool { return len(f.published.all()) == 5 }, 5*time.Second, 10*time.Millisecond)
}
