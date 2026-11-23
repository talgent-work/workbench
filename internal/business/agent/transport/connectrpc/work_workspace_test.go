package connectrpc_test

import (
	"context"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/stretchr/testify/require"

	agentprivatev1 "github.com/talgent-work/contracts/gen/go/agent/private/v1"
	agentpublicv1 "github.com/talgent-work/contracts/gen/go/agent/public/v1"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

func (f *accessFixture) browseOwn(t *testing.T, ctx context.Context, path string) (*agentpublicv1.BrowseWorkWorkspaceResponse, connect.Code) {
	t.Helper()
	response, err := f.publicWorkspaces.BrowseWorkWorkspace(ctx, &agentpublicv1.BrowseWorkWorkspaceRequest{WorkId: f.humanWork, Path: path})
	if err != nil {
		return nil, connect.CodeOf(err)
	}
	return response, 0
}

// A Work's owner and its Work Share holders read its workspace for
// themselves; nobody else does.
func TestReadersBrowseTheirWorkWorkspace(t *testing.T) {
	ctx := context.Background()
	f := newAccessFixtureWith(t, fakeWorkspaces{})

	listing, code := f.browseOwn(t, asUser(ctx, "owner"), "")
	require.Zero(t, code)
	require.Equal(t, agentpublicv1.WorkWorkspaceState_WORK_WORKSPACE_STATE_AVAILABLE, listing.GetState())
	require.Equal(t, uint32(2), listing.GetTotal())
	require.Equal(t, agentpublicv1.WorkWorkspaceEntryKind_WORK_WORKSPACE_ENTRY_KIND_DIRECTORY, listing.GetEntries()[0].GetKind())
	require.Equal(t, agentpublicv1.WorkWorkspaceEntryKind_WORK_WORKSPACE_ENTRY_KIND_LINK, listing.GetEntries()[1].GetKind(), "links are listed as links")
	inputs, _ := f.browseOwn(t, asUser(ctx, "owner"), "inputs")
	require.Equal(t, "brief.md", inputs.GetEntries()[0].GetAttachmentName(), "inputs/ entries name the Work's Attachments")

	_, code = f.browseOwn(t, asUser(ctx, "reader"), "")
	require.Equal(t, connect.CodePermissionDenied, code, "a person without a Work Share is refused")
	_, err := f.access.GrantWorkShare(authenticated(ctx, "control"), &agentprivatev1.GrantWorkShareRequest{WorkId: f.humanWork, RequesterId: "owner", ReaderId: "reader", Admission: memberOfProjectA})
	require.NoError(t, err)
	listing, code = f.browseOwn(t, asUser(ctx, "reader"), "")
	require.Zero(t, code)
	require.Equal(t, agentpublicv1.WorkWorkspaceState_WORK_WORKSPACE_STATE_AVAILABLE, listing.GetState(), "a Work Share holder reads it")

	preview, err := f.publicWorkspaces.GetWorkWorkspaceFilePreview(asUser(ctx, "reader"), &agentpublicv1.GetWorkWorkspaceFilePreviewRequest{WorkId: f.humanWork, Path: "notes.md"})
	require.NoError(t, err)
	require.Equal(t, agentpublicv1.WorkFilePreviewMode_WORK_FILE_PREVIEW_MODE_DIRECT, preview.GetPreview().GetMode())
	require.Equal(t, agentpublicv1.WorkFilePreviewDisplay_WORK_FILE_PREVIEW_DISPLAY_TEXT, preview.GetPreview().GetDisplay())
	require.Equal(t, "/content?grant=preview", preview.GetPreview().GetAccessUrl())
	download, err := f.publicWorkspaces.CreateWorkWorkspaceFileDownload(asUser(ctx, "reader"), &agentpublicv1.CreateWorkWorkspaceFileDownloadRequest{WorkId: f.humanWork, Path: "notes.md"})
	require.NoError(t, err)
	require.Equal(t, "/content?grant=download", download.GetAccessUrl())

	_, err = f.publicWorkspaces.BrowseWorkWorkspace(ctx, &agentpublicv1.BrowseWorkWorkspaceRequest{WorkId: f.humanWork})
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err), "an anonymous caller is refused before the Work is read")
}

// A workspace that cannot be read says why, with nothing to open.
func TestWorkWorkspaceStates(t *testing.T) {
	ctx := context.Background()
	f := newAccessFixtureWith(t, fakeWorkspaces{})
	f.sessions.items[f.humanWork].State = domain.WorkClosed
	listing, _ := f.browseOwn(t, asUser(ctx, "owner"), "")
	require.Equal(t, agentpublicv1.WorkWorkspaceState_WORK_WORKSPACE_STATE_CLOSED, listing.GetState())
	require.Empty(t, listing.GetEntries())
	preview, err := f.publicWorkspaces.GetWorkWorkspaceFilePreview(asUser(ctx, "owner"), &agentpublicv1.GetWorkWorkspaceFilePreviewRequest{WorkId: f.humanWork, Path: "notes.md"})
	require.NoError(t, err)
	require.Equal(t, agentpublicv1.WorkWorkspaceState_WORK_WORKSPACE_STATE_CLOSED, preview.GetState())
	require.Nil(t, preview.GetPreview())

	for missing, state := range map[error]agentpublicv1.WorkWorkspaceState{
		domain.ErrWorkspaceNotCreated:     agentpublicv1.WorkWorkspaceState_WORK_WORKSPACE_STATE_NOT_CREATED,
		application.ErrWorkspaceReclaimed: agentpublicv1.WorkWorkspaceState_WORK_WORKSPACE_STATE_RECLAIMED,
		domain.ErrWorkspaceUnavailable:    agentpublicv1.WorkWorkspaceState_WORK_WORKSPACE_STATE_UNAVAILABLE,
	} {
		f := newAccessFixtureWith(t, fakeWorkspaces{missing: missing})
		listing, _ := f.browseOwn(t, asUser(ctx, "owner"), "")
		require.Equal(t, state, listing.GetState())
	}
}
