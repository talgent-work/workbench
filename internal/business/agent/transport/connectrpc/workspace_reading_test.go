package connectrpc_test

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/stretchr/testify/require"

	agentprivatev1 "github.com/talgent-work/contracts/gen/go/agent/private/v1"
	agentpublicv1 "github.com/talgent-work/contracts/gen/go/agent/public/v1"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// fakeWorkspaces stands in for Storage's Workspace View and file links.
type fakeWorkspaces struct {
	application.WorkspaceReadingPort
	missing error
}

func (f fakeWorkspaces) WorkspaceView(_ context.Context, _ domain.WorkspaceLocation, path string) (application.WorkspaceListing, error) {
	if f.missing != nil {
		return application.WorkspaceListing{}, f.missing
	}
	if path == "inputs" {
		return application.WorkspaceListing{Total: 1, Entries: []application.WorkspaceEntry{{Name: "brief.md", Kind: application.WorkspaceFile, SizeBytes: 5, ModifiedAt: time.Unix(1, 0), AttachmentName: "brief.md"}}}, nil
	}
	return application.WorkspaceListing{Total: 2, Entries: []application.WorkspaceEntry{
		{Name: "inputs", Kind: application.WorkspaceDirectory}, {Name: "latest", Kind: application.WorkspaceLink},
	}}, nil
}

func (f fakeWorkspaces) PreviewWorkspaceFile(context.Context, domain.WorkspaceLocation, string) (application.WorkspaceFilePreview, error) {
	return application.WorkspaceFilePreview{Mode: application.WorkspacePreviewDirect, Display: application.WorkspacePreviewText, Link: &application.WorkspaceFileLink{URL: "/content?grant=preview", ExpiresAt: time.Unix(60, 0)}}, nil
}

func (f fakeWorkspaces) WorkspaceFileLink(context.Context, domain.WorkspaceLocation, string) (application.WorkspaceFileLink, error) {
	return application.WorkspaceFileLink{URL: "/content?grant=download", ExpiresAt: time.Unix(60, 0)}, nil
}

func (f *accessFixture) browse(t *testing.T, work, reader string, admission *agentprivatev1.ReaderAdmission, path string) (*agentprivatev1.BrowseWorkspaceResponse, connect.Code) {
	t.Helper()
	response, err := f.workspaces.BrowseWorkspace(authenticated(context.Background(), "control"), &agentprivatev1.BrowseWorkspaceRequest{WorkId: work, ReaderId: reader, Admission: admission, Path: path})
	if err != nil {
		return nil, connect.CodeOf(err)
	}
	return response, 0
}

// The workspace has the readers of the Work page, and no other.
func TestWorkspaceReadersAreTheWorkReaders(t *testing.T) {
	ctx := context.Background()
	f := newAccessFixtureWith(t, fakeWorkspaces{})

	listing, _ := f.browse(t, f.humanWork, "owner", memberOfProjectA, "")
	require.Equal(t, agentprivatev1.WorkspaceState_WORKSPACE_STATE_AVAILABLE, listing.GetState())
	require.Equal(t, agentprivatev1.WorkspaceEntryKind_WORKSPACE_ENTRY_KIND_LINK, listing.GetEntries()[1].GetKind(), "links are listed as links")
	_, code := f.browse(t, f.humanWork, "member", memberOfProjectA, "")
	require.Equal(t, connect.CodePermissionDenied, code, "a private Work's other members are refused, as on the Work page")
	_, code = f.admit(t, f.humanWork, "member", memberOfProjectA)
	require.Equal(t, connect.CodePermissionDenied, code, "the same refusal as reading the Work")

	_, err := f.access.GrantWorkShare(authenticated(ctx, "control"), &agentprivatev1.GrantWorkShareRequest{WorkId: f.humanWork, RequesterId: "owner", ReaderId: "reader", Admission: memberOfProjectA})
	require.NoError(t, err)
	listing, _ = f.browse(t, f.humanWork, "reader", memberOfProjectA, "")
	require.Equal(t, agentprivatev1.WorkspaceState_WORKSPACE_STATE_AVAILABLE, listing.GetState(), "a Work Share holder reads it")

	_, err = f.public.ChangeWorkVisibility(asUser(ctx, "owner"), &agentpublicv1.ChangeWorkVisibilityRequest{WorkId: f.humanWork, Public: true})
	require.NoError(t, err)
	listing, _ = f.browse(t, f.humanWork, "member", memberOfProjectA, "inputs")
	require.Equal(t, "brief.md", listing.GetEntries()[0].GetAttachmentName(), "inputs/ entries are the caller's Attachments")
	_, code = f.browse(t, f.humanWork, "member", &agentprivatev1.ReaderAdmission{Reference: "project", Value: "project-b"}, "")
	require.Equal(t, connect.CodePermissionDenied, code)

	preview, err := f.workspaces.PreviewWorkspaceFile(authenticated(ctx, "control"), &agentprivatev1.PreviewWorkspaceFileRequest{WorkId: f.humanWork, ReaderId: "member", Admission: memberOfProjectA, Path: "notes.md"})
	require.NoError(t, err)
	require.Equal(t, "/content?grant=preview", preview.GetAccessUrl())
	download, err := f.workspaces.DownloadWorkspaceFile(authenticated(ctx, "control"), &agentprivatev1.DownloadWorkspaceFileRequest{WorkId: f.humanWork, ReaderId: "member", Admission: memberOfProjectA, Path: "notes.md"})
	require.NoError(t, err)
	require.Equal(t, "/content?grant=download", download.GetAccessUrl())
	_, err = f.workspaces.BrowseWorkspace(ctx, &agentprivatev1.BrowseWorkspaceRequest{WorkId: f.humanWork, ReaderId: "owner"})
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err), "only a trusted caller reads on a reader's behalf")
}

// A closed Work's workspace is refused before reclamation finishes, and a
// workspace Storage never created or reclaimed says so.
func TestWorkspaceStates(t *testing.T) {
	f := newAccessFixtureWith(t, fakeWorkspaces{})
	f.sessions.items[f.humanWork].State = domain.WorkClosed
	listing, _ := f.browse(t, f.humanWork, "owner", memberOfProjectA, "")
	require.Equal(t, agentprivatev1.WorkspaceState_WORKSPACE_STATE_CLOSED, listing.GetState())
	require.Empty(t, listing.GetEntries())
	preview, err := f.workspaces.PreviewWorkspaceFile(authenticated(context.Background(), "control"), &agentprivatev1.PreviewWorkspaceFileRequest{WorkId: f.humanWork, ReaderId: "owner", Path: "notes.md"})
	require.NoError(t, err)
	require.Equal(t, agentprivatev1.WorkspaceState_WORKSPACE_STATE_CLOSED, preview.GetState())
	require.Empty(t, preview.GetAccessUrl())

	for missing, state := range map[error]agentprivatev1.WorkspaceState{
		domain.ErrWorkspaceNotCreated:     agentprivatev1.WorkspaceState_WORKSPACE_STATE_NOT_CREATED,
		application.ErrWorkspaceReclaimed: agentprivatev1.WorkspaceState_WORKSPACE_STATE_RECLAIMED,
		// A Managed Work's machine is away: its files come back with it.
		domain.ErrWorkspaceUnavailable: agentprivatev1.WorkspaceState_WORKSPACE_STATE_UNAVAILABLE,
	} {
		f := newAccessFixtureWith(t, fakeWorkspaces{missing: missing})
		listing, _ := f.browse(t, f.humanWork, "owner", nil, "")
		require.Equal(t, state, listing.GetState())
	}
}
