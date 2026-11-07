package application_test

import (
	"context"
	"io"
	"strings"
	"testing"

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

// machineLinks grants each token a read of the Work it names.
type machineLinks struct {
	application.WorkspacePort
	opened []string
}

func (m *machineLinks) LinkedWork(token string) (string, error) {
	if token == "forged" {
		return "", application.ErrInvalidWorkspaceFileLink
	}
	return token, nil
}

func (m *machineLinks) OpenLink(_ context.Context, token string) (application.OpenedWorkspaceFile, error) {
	m.opened = append(m.opened, token)
	return application.OpenedWorkspaceFile{Name: "notes.md", Content: io.NopCloser(strings.NewReader("# notes"))}, nil
}

// A link reads its Work's file until the Work is Closed; a Closed Work's link
// is refused before its machine is reached, as a forged link or a Work that
// no longer exists is.
func TestWorkspaceFileLinksAreRefusedOnceTheWorkIsClosed(t *testing.T) {
	files := &machineLinks{}
	links, err := application.NewWorkspaceFileLinks(linkedWorks{
		"running": {ID: "running", State: domain.WorkWorking},
		"closed":  {ID: "closed", State: domain.WorkClosed},
	}, files)
	require.NoError(t, err)

	opened, err := links.Open(context.Background(), "running")
	require.NoError(t, err)
	require.Equal(t, "notes.md", opened.Name)
	_, err = links.Open(context.Background(), "closed")
	require.ErrorIs(t, err, application.ErrWorkspaceClosed)
	_, err = links.Open(context.Background(), "forged")
	require.ErrorIs(t, err, application.ErrInvalidWorkspaceFileLink)
	_, err = links.Open(context.Background(), "gone")
	require.ErrorIs(t, err, application.ErrInvalidWorkspaceFileLink)
	require.Equal(t, []string{"running"}, files.opened)

	_, err = application.NewWorkspaceFileLinks(nil, files)
	require.Error(t, err)
}
