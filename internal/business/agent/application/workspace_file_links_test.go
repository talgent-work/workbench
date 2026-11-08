package application_test

import (
	"context"
	"io"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// linkedWorks holds Works by identity.
type linkedWorks map[string]*domain.AgentWork

func (linkedWorks) FindBySubject(context.Context, string, domain.WorkSubject) (*domain.AgentWork, error) {
	return nil, nil
}

func (w linkedWorks) Get(_ context.Context, id string) (*domain.AgentWork, error) {
	if work, ok := w[id]; ok {
		return work, nil
	}
	return nil, domain.ErrWorkNotFound
}

func (linkedWorks) Save(context.Context, *domain.AgentWork) error { return nil }

// machineFiles opens each linked file where its Work is located.
type machineFiles struct {
	application.WorkspaceReadingPort
	opened []domain.WorkspaceLocation
}

func (m *machineFiles) OpenWorkspaceFile(_ context.Context, location domain.WorkspaceLocation, grant application.WorkspaceFileGrant) (application.OpenedWorkspaceFile, error) {
	m.opened = append(m.opened, location)
	return application.OpenedWorkspaceFile{Name: grant.Path, Content: io.NopCloser(strings.NewReader("# notes"))}, nil
}

func grantOf(t *testing.T, link application.WorkspaceFileLink) string {
	t.Helper()
	parsed, err := url.Parse(link.URL)
	require.NoError(t, err)
	return parsed.Query().Get("grant")
}

// A link reads its Work's file where the Work is located, until the Work is
// Closed; a Closed Work's link is refused before its files are reached, as a
// forged link, a Work that no longer exists or a disconnected machine is.
func TestWorkspaceFileLinksAreRefusedOnceTheWorkIsClosed(t *testing.T) {
	signer, err := application.NewWorkspaceFileSigner("/api/v1/agent/workspace-files", "secret", time.Now)
	require.NoError(t, err)
	files := &machineFiles{}
	running := &domain.AgentWork{ID: "running", State: domain.WorkWorking, ComputerID: "laptop", ComputerKind: domain.ComputerManaged}
	away := &domain.AgentWork{ID: "away", State: domain.WorkWorking, ComputerID: "laptop", ComputerKind: domain.ComputerManaged, DisconnectedSince: time.Now()}
	links, err := application.NewWorkspaceFileLinks(linkedWorks{
		"running": running, "away": away,
		"closed": {ID: "closed", State: domain.WorkClosed, ComputerID: "laptop", ComputerKind: domain.ComputerManaged},
	}, files, signer)
	require.NoError(t, err)
	link := func(work string) string {
		return grantOf(t, signer.Issue(application.WorkspaceFileGrant{WorkID: work, Path: "notes.md"}))
	}

	opened, err := links.Open(context.Background(), link("running"))
	require.NoError(t, err)
	require.Equal(t, "notes.md", opened.Name)
	_, err = links.Open(context.Background(), link("closed"))
	require.ErrorIs(t, err, application.ErrWorkspaceClosed)
	_, err = links.Open(context.Background(), link("away"))
	require.ErrorIs(t, err, domain.ErrWorkspaceUnavailable)
	_, err = links.Open(context.Background(), link("running")+"x")
	require.ErrorIs(t, err, application.ErrInvalidWorkspaceFileLink)
	_, err = links.Open(context.Background(), link("gone"))
	require.ErrorIs(t, err, application.ErrInvalidWorkspaceFileLink)
	require.Equal(t, []domain.WorkspaceLocation{{WorkID: "running", ComputerID: "laptop"}}, files.opened)

	_, err = application.NewWorkspaceFileLinks(nil, files, signer)
	require.Error(t, err)
}

// A link reads only what it was signed for, until it expires.
func TestWorkspaceFileLinksRefuseAlteredAndExpiredGrants(t *testing.T) {
	now := time.Now()
	signer, err := application.NewWorkspaceFileSigner("/api/v1/agent/workspace-files", "secret", func() time.Time { return now })
	require.NoError(t, err)
	link := signer.Issue(application.WorkspaceFileGrant{WorkID: "w", Path: "a.txt"})
	require.Equal(t, now.UTC().Add(15*time.Minute).Truncate(time.Second), link.ExpiresAt)
	token := grantOf(t, link)
	grant, err := signer.Verify(token)
	require.NoError(t, err)
	require.Equal(t, application.WorkspaceFileGrant{WorkID: "w", Path: "a.txt", ExpiresAt: link.ExpiresAt.Unix()}, grant)
	encoded, signature, _ := strings.Cut(token, ".")
	other, _ := application.NewWorkspaceFileSigner("/api/v1/agent/workspace-files", "another", func() time.Time { return now })
	for _, forged := range []string{"", encoded, encoded + ".x", strings.ToUpper(encoded) + "." + signature} {
		_, err = signer.Verify(forged)
		require.ErrorIs(t, err, application.ErrInvalidWorkspaceFileLink, forged)
	}
	_, err = other.Verify(token)
	require.ErrorIs(t, err, application.ErrInvalidWorkspaceFileLink, "another key verifies nothing")
	now = now.Add(15*time.Minute + time.Second)
	_, err = signer.Verify(token)
	require.ErrorIs(t, err, application.ErrInvalidWorkspaceFileLink, "an expired link reads nothing")
	for _, address := range []string{"", "/", "/x?y=1"} {
		_, err = application.NewWorkspaceFileSigner(address, "secret", time.Now)
		require.Error(t, err, address)
	}
	_, err = application.NewWorkspaceFileSigner("/x", " ", time.Now)
	require.Error(t, err)
}
