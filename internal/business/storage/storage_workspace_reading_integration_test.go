//go:build integration

package storage

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/storage/application"
	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

// Workspace files read through Access Links, inline for a preview and as a
// download under their own name and type; links, paths out of the workspace
// and Attachments under inputs/ are not opened here.
func TestWorkspaceFilesReadThroughAccessLinks(t *testing.T) {
	f := newFixtureWith(t, func(config *Config) { config.Preview.TextBytes = 16 })
	f.workspaceFile("ws-1", "reports/季度 报告.md", "# Quarterly report, long enough to be cut")
	f.workspaceFile("ws-1", "deck.pptx", fixtureFile(t, "deck.pptx"))
	f.workspaceFile("ws-1", "outside-target.txt", "secret")
	directory, err := f.nas.PrepareWorkspace(t.Context(), "ws-1", nil)
	require.NoError(t, err)
	require.NoError(t, os.Symlink("outside-target.txt", filepath.Join(f.working, directory, "latest")))

	preview, err := f.app.PreviewWorkspaceFile(t.Context(), application.ViewedWorkspace{Reference: "ws-1"}, "reports/季度 报告.md")
	require.NoError(t, err)
	require.Equal(t, domain.PreviewVerdict{Mode: domain.PreviewTruncated, Display: domain.PreviewDisplayText, LimitBytes: 16}, preview.Verdict)
	status, body, header := f.get(preview.Link.URL)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "# Quarterly report, long enough to be cut", body)
	require.True(t, strings.HasPrefix(header.Get("Content-Disposition"), "inline"))
	stale, err := f.app.PreviewWorkspaceFile(t.Context(), application.ViewedWorkspace{Reference: "ws-1"}, "reports/季度 报告.md")
	require.NoError(t, err)
	f.workspaceFile("ws-1", "reports/季度 报告.md", "# Rewritten by the Agent meanwhile, now much longer than before")
	status, _, _ = f.get(stale.Link.URL)
	require.Equal(t, http.StatusConflict, status, "a preview link never serves a file changed since its verdict")

	office, err := f.app.PreviewWorkspaceFile(t.Context(), application.ViewedWorkspace{Reference: "ws-1"}, "deck.pptx")
	require.NoError(t, err)
	require.Equal(t, domain.PreviewVerdict{Mode: domain.PreviewRefused, Refusal: domain.PreviewRefusalUnsupported}, office.Verdict, "workspace office files are not rendered")
	require.Nil(t, office.Link)
	link, err := f.app.IssueWorkspaceFileLink(t.Context(), application.ViewedWorkspace{Reference: "ws-1"}, "deck.pptx")
	require.NoError(t, err)
	status, body, header = f.get(link.URL)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, fixtureFile(t, "deck.pptx"), body, "the original bytes")
	require.Equal(t, `attachment; filename=deck.pptx`, header.Get("Content-Disposition"))

	for _, refused := range []string{"latest", "../escape", "/../etc/passwd", "inputs/brief.md", "reports"} {
		_, err := f.app.PreviewWorkspaceFile(t.Context(), application.ViewedWorkspace{Reference: "ws-1"}, refused)
		require.ErrorIs(t, err, application.ErrInvalidWorkspacePath, refused)
	}
	_, err = f.app.PreviewWorkspaceFile(t.Context(), application.ViewedWorkspace{Reference: "ws-1"}, "absent.md")
	require.ErrorIs(t, err, application.ErrFileNotFound)
}

// Listing marks links, caps a large directory, and tells a workspace never
// created from one reclaimed.
func TestWorkspaceListingAndAbsence(t *testing.T) {
	f := newFixtureWith(t, func(config *Config) { config.WorkspaceListEntries = 4 })
	for i := range 5 {
		f.workspaceFile("ws-1", "many/"+strconv.Itoa(i)+".log", "x")
	}
	f.workspaceFile("ws-1", ".hidden", "h")
	directory, err := f.nas.PrepareWorkspace(t.Context(), "ws-1", nil)
	require.NoError(t, err)
	require.NoError(t, os.Symlink("/etc", filepath.Join(f.working, directory, "link")))

	root, err := f.app.WorkspaceView(t.Context(), application.ViewedWorkspace{Reference: "ws-1"}, "")
	require.NoError(t, err)
	kinds := map[string]application.WorkspaceEntryKind{}
	for _, entry := range root.Entries {
		kinds[entry.Name] = entry.Kind
	}
	require.Equal(t, map[string]application.WorkspaceEntryKind{".hidden": application.WorkspaceFile, "inputs": application.WorkspaceDirectory, "link": application.WorkspaceLink, "many": application.WorkspaceDirectory}, kinds, "hidden files and links are listed")
	many, err := f.app.WorkspaceView(t.Context(), application.ViewedWorkspace{Reference: "ws-1"}, "many")
	require.NoError(t, err)
	require.Len(t, many.Entries, 4)
	require.Equal(t, 5, many.Total)
	require.True(t, many.Capped())
	require.Equal(t, "0.log", many.Entries[0].Name, "by name")
	_, err = f.app.WorkspaceView(t.Context(), application.ViewedWorkspace{Reference: "ws-1"}, "link")
	require.Error(t, err, "a link is never followed")

	_, err = f.app.WorkspaceView(t.Context(), application.ViewedWorkspace{Reference: "never-started"}, "")
	require.ErrorIs(t, err, application.ErrWorkspaceNotCreated)
	require.NoError(t, f.app.RequestWorkspaceReclamation(t.Context(), "work-1", "ws-1"))
	_, err = f.app.WorkspaceView(t.Context(), application.ViewedWorkspace{Reference: "ws-1"}, "")
	require.NoError(t, err, "a requested reclamation leaves the workspace until it is carried out")
	require.NoError(t, f.app.ReclaimRequestedWorkspaces(t.Context()))
	_, err = f.app.WorkspaceView(t.Context(), application.ViewedWorkspace{Reference: "ws-1"}, "")
	require.ErrorIs(t, err, application.ErrWorkspaceReclaimed)
	_, err = f.app.IssueWorkspaceFileLink(t.Context(), application.ViewedWorkspace{Reference: "ws-1"}, "many/0.log")
	require.ErrorIs(t, err, application.ErrWorkspaceReclaimed)
	require.NoError(t, f.app.ReclaimRequestedWorkspaces(t.Context()), "a later run keeps the reclaimed record")
	_, err = f.app.WorkspaceView(t.Context(), application.ViewedWorkspace{Reference: "ws-1"}, "")
	require.ErrorIs(t, err, application.ErrWorkspaceReclaimed)
}
