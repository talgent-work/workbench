//go:build integration

package agent_test

import (
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	agentprivatev1 "github.com/talgent-work/contracts/gen/go/agent/private/v1"
	"github.com/talgent-work/contracts/gen/go/agent/private/v1/agentprivatev1connect"
	agentpublicv1 "github.com/talgent-work/contracts/gen/go/agent/public/v1"
	"github.com/talgent-work/contracts/gen/go/agent/public/v1/agentpublicv1connect"
)

// files reaches the public Work file API; each call carries its caller's identity.
func (m *lifecycleModule) files() agentpublicv1connect.WorkFileServiceClient {
	return agentpublicv1connect.NewWorkFileServiceClient(connect.NewClient(connecthttp.NewTransport(m.public, m.url, connecthttp.WithReadMaxBytes(0))))
}

// An Application uploads, renames and deletes its own Work's Attachments,
// attributed to itself, reads and downloads its files, and shares them by
// Share Link, as their owner.
func TestAnApplicationUsesItsOwnWorksFiles(t *testing.T) {
	m := newLifecycleModule(t).start()
	desk := m.as("application", supportDesk)
	files := m.files()
	work, err := desk.start(t, uuid.NewString(), "Answer ticket 42")
	require.NoError(t, err)

	_, err = files.ListWorkFiles(desk.ctx, &agentpublicv1.ListWorkFilesRequest{WorkId: work})
	require.NoError(t, err)
	upload, err := files.CreateWorkAttachmentUpload(desk.ctx, &agentpublicv1.CreateWorkAttachmentUploadRequest{WorkId: work, Name: "ticket.md"})
	require.NoError(t, err)
	require.Contains(t, upload.GetUploadUrl(), "user=&application="+supportDesk, "the upload is the Application's, not any person's")
	_, err = files.RenameWorkAttachment(desk.ctx, &agentpublicv1.RenameWorkAttachmentRequest{WorkId: work, Name: "ticket.md", NewName: "ticket-42.md"})
	require.NoError(t, err)
	_, err = files.DeleteWorkFile(desk.ctx, &agentpublicv1.DeleteWorkFileRequest{WorkId: work, Kind: agentpublicv1.WorkFileKind_WORK_FILE_KIND_ATTACHMENT, Name: "ticket-42.md"})
	require.NoError(t, err)
	link, err := files.CreateWorkFileAccessLink(desk.ctx, &agentpublicv1.CreateWorkFileAccessLinkRequest{WorkId: work, Kind: agentpublicv1.WorkFileKind_WORK_FILE_KIND_ARTIFACT, Name: "answer.md"})
	require.NoError(t, err)
	require.Contains(t, link.GetAccessUrl(), "answer.md")

	shared, err := files.CreateWorkFileShareLink(desk.ctx, &agentpublicv1.CreateWorkFileShareLinkRequest{WorkId: work, Kind: agentpublicv1.WorkFileKind_WORK_FILE_KIND_ARTIFACT, Name: "answer.md"})
	require.NoError(t, err)
	require.Equal(t, supportDesk, shared.GetShareLink().GetCreatedByApplicationId(), "the Application shares as itself")
	require.Empty(t, shared.GetShareLink().GetCreatedByUserId())
	_, err = files.StopWorkFileSharing(desk.ctx, &agentpublicv1.StopWorkFileSharingRequest{WorkId: work, Kind: agentpublicv1.WorkFileKind_WORK_FILE_KIND_ARTIFACT, Name: "answer.md"})
	require.NoError(t, err)
}

// Another Application and a person reach none of an Application's Work's
// files, and the Application none of a person's.
func TestAnApplicationReachesOnlyItsOwnWorksFiles(t *testing.T) {
	m := newLifecycleModule(t).start()
	desk, billing := m.as("application", supportDesk), m.as("application", billingBot)
	person := m.as("user", uuid.NewString())
	work, err := desk.start(t, uuid.NewString(), "Answer ticket 42")
	require.NoError(t, err)
	personal, err := person.start(t, uuid.NewString(), "Draft my plan")
	require.NoError(t, err)

	denied := func(err error) { t.Helper(); require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err)) }
	reach := func(c ownerClients, workID string) {
		t.Helper()
		files := m.files()
		_, err := files.ListWorkFiles(c.ctx, &agentpublicv1.ListWorkFilesRequest{WorkId: workID})
		denied(err)
		_, err = files.CreateWorkAttachmentUpload(c.ctx, &agentpublicv1.CreateWorkAttachmentUploadRequest{WorkId: workID, Name: "x.md"})
		denied(err)
		_, err = files.DeleteWorkFile(c.ctx, &agentpublicv1.DeleteWorkFileRequest{WorkId: workID, Kind: agentpublicv1.WorkFileKind_WORK_FILE_KIND_ARTIFACT, Name: "answer.md"})
		denied(err)
		_, err = files.CreateWorkFileAccessLink(c.ctx, &agentpublicv1.CreateWorkFileAccessLinkRequest{WorkId: workID, Kind: agentpublicv1.WorkFileKind_WORK_FILE_KIND_ARTIFACT, Name: "answer.md"})
		denied(err)
		_, err = files.CreateWorkFileShareLink(c.ctx, &agentpublicv1.CreateWorkFileShareLinkRequest{WorkId: workID, Kind: agentpublicv1.WorkFileKind_WORK_FILE_KIND_ARTIFACT, Name: "answer.md"})
		denied(err)
		_, err = files.StopWorkFileSharing(c.ctx, &agentpublicv1.StopWorkFileSharingRequest{WorkId: workID, Kind: agentpublicv1.WorkFileKind_WORK_FILE_KIND_ARTIFACT, Name: "answer.md"})
		denied(err)
	}
	reach(billing, work)
	reach(person, work)
	reach(desk, personal)
}

// Agent admits an Application to its own Work's message stream as its
// owner, and refuses another Application and a person.
func TestAnApplicationReadsOnlyItsOwnWorksStream(t *testing.T) {
	m := newLifecycleModule(t).start()
	work, err := m.as("application", supportDesk).start(t, uuid.NewString(), "Answer ticket 42")
	require.NoError(t, err)
	access := agentprivatev1connect.NewWorkAccessServiceClient(connect.NewClient(connecthttp.NewTransport(m.public, m.url, connecthttp.WithReadMaxBytes(0))))

	admitted, err := access.AdmitWorkReader(sessionControlRequest(m.ctx), &agentprivatev1.AdmitWorkReaderRequest{WorkId: work, ReaderId: supportDesk, IncludeWorkState: true})
	require.NoError(t, err)
	require.Equal(t, agentprivatev1.WorkReadBasis_WORK_READ_BASIS_OWNER, admitted.GetBasis())
	for _, reader := range []string{billingBot, uuid.NewString()} {
		_, err = access.AdmitWorkReader(sessionControlRequest(m.ctx), &agentprivatev1.AdmitWorkReaderRequest{WorkId: work, ReaderId: reader})
		require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err), reader)
	}
}
