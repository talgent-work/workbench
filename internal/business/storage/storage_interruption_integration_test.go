//go:build integration

package storage

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/storage/application"
	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

// interruptibleSpaces is the real repository whose saves can be made to fail.
type interruptibleSpaces struct {
	domain.FileSpaceRepository
	failing atomic.Bool
}

var errInterrupted = errors.New("interrupted before the record")

func (s *interruptibleSpaces) Save(ctx context.Context, space *domain.FileSpace) error {
	if s.failing.Load() {
		return errInterrupted
	}
	return s.FileSpaceRepository.Save(ctx, space)
}

// A rename that cannot be recorded is moved back, so its Share Links keep
// reaching the file.
func TestUnrecordedRenameIsUndone(t *testing.T) {
	f := newFixture(t)
	space := f.open("case-1")
	require.Equal(t, http.StatusOK, f.upload("case-1", space.SpaceToken, "brief.md", "v1", false).StatusCode)
	share, err := f.app.CreateShareLink(t.Context(), "case-1", domain.FileKindAttachment, "brief.md", "user-1")
	require.NoError(t, err)

	f.spaces.failing.Store(true)
	_, err = f.app.RenameAttachment(t.Context(), "case-1", space.SpaceToken, "brief.md", "final.md", "user-1")
	require.ErrorIs(t, err, errInterrupted)
	f.spaces.failing.Store(false)
	files := f.files("case-1")
	require.Contains(t, files, "attachment/brief.md")
	require.NotContains(t, files, "attachment/final.md")
	status, body, _ := f.get(share.URL)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "v1", body)
}

// A deletion that cannot be recorded leaves its link broken, never pointing
// at a later file of the same name: a create revokes it first, while links
// shared once the new file exists stay.
func TestLinksLeftByAnUnrecordedDeletionNeverReachANewFile(t *testing.T) {
	f := newFixture(t)
	space := f.open("case-1")
	require.Equal(t, http.StatusOK, f.upload("case-1", space.SpaceToken, "brief.md", "secret", false).StatusCode)
	attachment, err := f.app.CreateShareLink(t.Context(), "case-1", domain.FileKindAttachment, "brief.md", "user-1")
	require.NoError(t, err)
	f.workspaceFile("ws-1", "report.md", "old report")
	source := application.ArtifactSource{SpaceToken: space.SpaceToken, WorkID: "work-1", WorkspaceReference: "ws-1", Path: "report.md"}
	_, err = f.app.PublishArtifact(t.Context(), source, "report.md", false)
	require.NoError(t, err)
	artifact, err := f.app.CreateShareLink(t.Context(), "case-1", domain.FileKindArtifact, "report.md", "user-1")
	require.NoError(t, err)

	f.spaces.failing.Store(true)
	require.ErrorIs(t, f.app.DeleteFile(t.Context(), "case-1", space.SpaceToken, domain.FileKindAttachment, "brief.md", "user-1"), errInterrupted)
	require.ErrorIs(t, f.app.DeleteArtifact(t.Context(), space.SpaceToken, "report.md", "work-1"), errInterrupted)
	f.spaces.failing.Store(false)
	status, _, _ := f.get(attachment.URL)
	require.Equal(t, http.StatusNotFound, status, "the link is broken, not revoked")

	require.Equal(t, http.StatusOK, f.upload("case-1", space.SpaceToken, "brief.md", "public", false).StatusCode)
	f.workspaceFile("ws-1", "report.md", "new report")
	_, err = f.app.PublishArtifact(t.Context(), source, "report.md", false)
	require.NoError(t, err)
	for _, link := range []string{attachment.URL, artifact.URL} {
		status, _, _ = f.get(link)
		require.Equal(t, http.StatusGone, status, "a link to the deleted file never opens the new one")
	}
	fresh, err := f.app.CreateShareLink(t.Context(), "case-1", domain.FileKindAttachment, "brief.md", "user-1")
	require.NoError(t, err)
	status, body, _ := f.get(fresh.URL)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "public", body)

	// Retrying the interrupted deletion records it.
	require.NoError(t, f.app.DeleteFile(t.Context(), "case-1", space.SpaceToken, domain.FileKindAttachment, "brief.md", "user-1"))
	status, _, _ = f.get(fresh.URL)
	require.Equal(t, http.StatusGone, status)
}
