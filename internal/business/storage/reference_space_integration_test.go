//go:build integration

package storage

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/storage/application"
	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

func referenceFile(t *testing.T, path, content string) application.ReferenceFile {
	t.Helper()
	parsed, err := domain.ParseReferencePath(path)
	require.NoError(t, err)
	return application.ReferenceFile{Path: parsed, Content: []byte(content)}
}

// guides is a Reference Space mount the tests' caller names.
var guides = []domain.ReferenceMount{{Name: "guides", Owner: "project-1"}}

// A Work mounts a Reference Space read-only at the name its caller gave; it
// is empty until the first replacement, and the mount shows whichever content
// is current after each later one. No earlier content stays readable.
func TestReferenceSpaceIsReplacedWholeUnderAStandingMount(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	mounts, err := f.app.AcquireMounts(ctx, application.MountRequest{HomeOwner: "user-1", WorkID: "work-1", WorkspaceReference: "ws-1", References: guides})
	require.NoError(t, err)
	require.Equal(t, []application.MountedReference{{Name: "guides", Grant: application.MountGrant{ClaimName: "juicefs-inputs", SubPath: "talgent/assets/references/project-1", ReadOnly: true}}}, mounts.References)
	require.DirExists(t, filepath.Join(f.working, mounts.Workspace.SubPath, "guides"), "the mount point is prepared in the workspace")
	// The sandbox sees the space through its grant.
	mounted := filepath.Join(f.persistent, "assets/references/project-1")
	files, err := f.app.ReadReferenceFiles(ctx, "project-1", nil)
	require.NoError(t, err)
	require.Empty(t, files)
	visible := func() []string {
		entries, err := os.ReadDir(mounted)
		require.NoError(t, err)
		var names []string
		for _, entry := range entries {
			if !strings.HasPrefix(entry.Name(), ".") {
				names = append(names, entry.Name())
			}
		}
		return names
	}
	require.Empty(t, visible(), "empty before the first content")

	first := []application.ReferenceFile{referenceFile(t, "index.md", "one"), referenceFile(t, "pages/a.md", "a"), referenceFile(t, ".handbook.json", "{}")}
	require.NoError(t, f.app.ReplaceReferenceContent(ctx, "project-1", first))
	require.NoError(t, f.app.ReplaceReferenceContent(ctx, "project-1", first), "replacing with the same content again succeeds")
	content, err := os.ReadFile(filepath.Join(mounted, "index.md"))
	require.NoError(t, err)
	require.Equal(t, "one", string(content))
	require.FileExists(t, filepath.Join(mounted, "pages", "a.md"))
	require.ElementsMatch(t, []string{"index.md", "pages"}, visible(), "the writer's hidden files stay out of sight")

	require.NoError(t, f.app.ReplaceReferenceContent(ctx, "project-1", []application.ReferenceFile{referenceFile(t, "index.md", "two"), referenceFile(t, "guide.md", "g")}))
	contents, err := os.ReadDir(filepath.Join(mounted, ".contents"))
	require.NoError(t, err)
	require.Len(t, contents, 1, "only the current content is kept")
	content, err = os.ReadFile(filepath.Join(mounted, "index.md"))
	require.NoError(t, err)
	require.Equal(t, "two", string(content), "a standing mount follows the replacement")
	require.NoFileExists(t, filepath.Join(mounted, "pages", "a.md"), "a file left out of the new content is gone")
	require.ElementsMatch(t, []string{"index.md", "guide.md"}, visible(), "the entries are the new content's")
	require.NoDirExists(t, filepath.Join(f.persistent, "assets/references-retired/project-1"), "earlier content is removed before the replacement completes")

	files, err = f.app.ReadReferenceFiles(ctx, "project-1", []string{"index.md", "pages/a.md"})
	require.NoError(t, err)
	require.Len(t, files, 1)

	require.ErrorIs(t, f.app.ReplaceReferenceContent(ctx, "project-1", []application.ReferenceFile{{Path: "../escape", Content: []byte("x")}}), domain.ErrInvalidReferencePath)
	require.ErrorIs(t, f.app.ReplaceReferenceContent(ctx, "a/b", nil), domain.ErrInvalidOwner)
	_, err = f.app.AcquireMounts(ctx, application.MountRequest{HomeOwner: "user-1", WorkID: "work-1", References: []domain.ReferenceMount{{Name: "inputs", Owner: "project-1"}}})
	require.ErrorIs(t, err, domain.ErrInvalidReferenceMount)
}

// The Workspace View shows a mount as the Work's sandbox sees it: the
// visible entries of the Reference Space's current content, read and
// downloaded through Access Links bound to what was judged.
func TestWorkspaceViewShowsAReferenceMount(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	_, err := f.app.AcquireMounts(ctx, application.MountRequest{HomeOwner: "user-1", WorkID: "work-1", WorkspaceReference: "ws-1", References: guides})
	require.NoError(t, err)
	workspace := application.ViewedWorkspace{Reference: "ws-1", References: guides}
	names := func(directory string) map[string]application.WorkspaceEntryKind {
		listing, err := f.app.WorkspaceView(ctx, workspace, directory)
		require.NoError(t, err)
		kinds := map[string]application.WorkspaceEntryKind{}
		for _, entry := range listing.Entries {
			kinds[entry.Name] = entry.Kind
		}
		return kinds
	}
	require.Equal(t, application.WorkspaceDirectory, names("")["guides"])
	require.Empty(t, names("guides"), "empty before the first content")
	_, err = f.app.WorkspaceView(ctx, workspace, "guides/pages")
	require.ErrorIs(t, err, application.ErrFileNotFound)

	require.NoError(t, f.app.ReplaceReferenceContent(ctx, "project-1", []application.ReferenceFile{referenceFile(t, "index.md", "one"), referenceFile(t, "pages/a.md", "a"), referenceFile(t, ".handbook.json", "{}")}))
	require.Equal(t, map[string]application.WorkspaceEntryKind{"index.md": application.WorkspaceFile, "pages": application.WorkspaceDirectory}, names("guides"), "the current content's visible entries, not the space's links")
	require.Equal(t, map[string]application.WorkspaceEntryKind{"a.md": application.WorkspaceFile}, names("/guides/pages/"))

	preview, err := f.app.PreviewWorkspaceFile(ctx, workspace, "guides/index.md")
	require.NoError(t, err)
	require.NotNil(t, preview.Link)
	status, body, _ := f.get(preview.Link.URL)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "one", body)
	link, err := f.app.IssueWorkspaceFileLink(ctx, workspace, "guides/pages/a.md")
	require.NoError(t, err)
	status, body, header := f.get(link.URL)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "a", body)
	require.Equal(t, `attachment; filename=a.md`, header.Get("Content-Disposition"))
	for _, refused := range []string{"guides/.handbook.json", "guides/.current/index.md", "guides/pages", "guides"} {
		_, err := f.app.PreviewWorkspaceFile(ctx, workspace, refused)
		require.ErrorIs(t, err, application.ErrInvalidWorkspacePath, refused)
	}

	require.NoError(t, f.app.ReplaceReferenceContent(ctx, "project-1", []application.ReferenceFile{referenceFile(t, "index.md", "two, longer")}))
	status, _, _ = f.get(preview.Link.URL)
	require.Equal(t, http.StatusConflict, status, "a preview link never serves a file a later content changed")
	status, body, _ = f.get(link.URL)
	require.Equal(t, http.StatusNotFound, status, "a download reads the current content: %s", body)

	// Without the mount the name is an ordinary workspace path.
	bare := application.ViewedWorkspace{Reference: "ws-1"}
	listing, err := f.app.WorkspaceView(ctx, bare, "guides")
	require.NoError(t, err)
	require.Empty(t, listing.Entries, "the empty mount point on working storage")
}
