//go:build integration

package agent_test

import (
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	integrationmessage "github.com/go-jimu/components/ddd/message"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	accountintegrationv1 "github.com/talgent-work/contracts/gen/go/account/integration/v1"
	agentprivatev1 "github.com/talgent-work/contracts/gen/go/agent/private/v1"
	"github.com/talgent-work/contracts/gen/go/agent/private/v1/agentprivatev1connect"
	agentpublicv1 "github.com/talgent-work/contracts/gen/go/agent/public/v1"
	agentpublicv2 "github.com/talgent-work/contracts/gen/go/agent/public/v2"

	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/transport/messagehandler"
)

// listedFor lists the Works the caller reads for the Application, or the
// listing's refusal.
func (c ownerClients) listedFor(applicationID string, labels map[string]string) ([]string, error) {
	response, err := c.works.ListWorks(c.ctx, &agentpublicv2.ListWorksRequest{ApplicationId: applicationID, Labels: labels})
	var ids []string
	for _, work := range response.GetWorks() {
		ids = append(ids, work.GetWorkId())
	}
	return ids, err
}

// The Application Owner lists the Application's Works, narrowed by a Work
// Label, and never their own among them; another person and another
// Application list none of them.
func TestTheApplicationOwnerListsTheApplicationsWorks(t *testing.T) {
	m := newLifecycleModule(t).start()
	owner, desk := m.as("user", supportDeskOwner), m.as("application", supportDesk)
	stranger, billing := m.as("user", uuid.NewString()), m.as("application", billingBot)

	ticket, err := desk.works.StartWork(desk.ctx, &agentpublicv2.StartWorkRequest{RequestId: uuid.NewString(), InitialMessage: "Answer ticket 42", Labels: map[string]string{"tenant": "acme"}})
	require.NoError(t, err)
	other, err := desk.works.StartWork(desk.ctx, &agentpublicv2.StartWorkRequest{RequestId: uuid.NewString(), InitialMessage: "Answer ticket 43", Labels: map[string]string{"tenant": "globex"}})
	require.NoError(t, err)
	own, err := owner.start(t, uuid.NewString(), "Draft my plan")
	require.NoError(t, err)

	listed, err := owner.listedFor(supportDesk, nil)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{ticket.GetWorkId(), other.GetWorkId()}, listed)
	listed, err = owner.listedFor(supportDesk, map[string]string{"tenant": "acme"})
	require.NoError(t, err)
	require.Equal(t, []string{ticket.GetWorkId()}, listed)
	require.Equal(t, []string{own}, owner.listed(t), "the owner's own Works stay apart")

	listed, err = desk.listedFor(supportDesk, nil)
	require.NoError(t, err, "an Application names itself")
	require.Len(t, listed, 2)
	for _, refused := range []ownerClients{stranger, billing} {
		_, err = refused.listedFor(supportDesk, nil)
		require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	}
	_, err = owner.listedFor(uuid.NewString(), nil)
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err), "an unknown Application is no one's")
	listed, err = owner.listedFor(retiredReporting, nil)
	require.NoError(t, err, "a removed Application's owner still reads its Works")
	require.Empty(t, listed)
}

// The Application Owner reads the Application's Work's message stream and
// files, also after the Application's removal, and drives none of it;
// another person reads none.
func TestTheApplicationOwnerReadsButNeverDrivesTheApplicationsWork(t *testing.T) {
	m := newLifecycleModule(t).start()
	strangerID := uuid.NewString()
	owner, desk, stranger := m.as("user", supportDeskOwner), m.as("application", supportDesk), m.as("user", strangerID)
	work, err := desk.start(t, uuid.NewString(), "Answer ticket 42")
	require.NoError(t, err)
	access := agentprivatev1connect.NewWorkAccessServiceClient(connect.NewClient(connecthttp.NewTransport(m.public, m.url, connecthttp.WithReadMaxBytes(0))))
	files := m.files()

	readable := func() {
		t.Helper()
		admitted, err := access.AdmitWorkReader(sessionControlRequest(m.ctx), &agentprivatev1.AdmitWorkReaderRequest{WorkId: work, ReaderId: supportDeskOwner, IncludeWorkState: true})
		require.NoError(t, err)
		require.Equal(t, agentprivatev1.WorkReadBasis_WORK_READ_BASIS_APPLICATION_OWNER, admitted.GetBasis())
		_, err = files.ListWorkFiles(owner.ctx, &agentpublicv1.ListWorkFilesRequest{WorkId: work})
		require.NoError(t, err)
		_, err = files.CreateWorkFileAccessLink(owner.ctx, &agentpublicv1.CreateWorkFileAccessLinkRequest{WorkId: work, Kind: agentpublicv1.WorkFileKind_WORK_FILE_KIND_ARTIFACT, Name: "answer.md"})
		require.NoError(t, err)
	}
	readable()

	denied := func(err error) { t.Helper(); require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err)) }
	_, err = owner.work.GetWork(owner.ctx, &agentpublicv1.GetWorkRequest{WorkId: work})
	denied(err)
	_, err = owner.work.SendMessage(owner.ctx, &agentpublicv1.SendMessageRequest{WorkId: work, InputId: uuid.NewString(), Text: "hijack"})
	denied(err)
	_, err = owner.work.StopWork(owner.ctx, &agentpublicv1.StopWorkRequest{WorkId: work})
	denied(err)
	_, err = owner.work.ArchiveWork(owner.ctx, &agentpublicv1.ArchiveWorkRequest{WorkId: work})
	denied(err)
	_, err = owner.works.ChangeWorkTitle(owner.ctx, &agentpublicv2.ChangeWorkTitleRequest{WorkId: work, Title: "Mine"})
	denied(err)
	_, err = files.CreateWorkAttachmentUpload(owner.ctx, &agentpublicv1.CreateWorkAttachmentUploadRequest{WorkId: work, Name: "x.md"})
	denied(err)
	_, err = files.DeleteWorkFile(owner.ctx, &agentpublicv1.DeleteWorkFileRequest{WorkId: work, Kind: agentpublicv1.WorkFileKind_WORK_FILE_KIND_ARTIFACT, Name: "answer.md"})
	denied(err)
	_, err = files.CreateWorkFileShareLink(owner.ctx, &agentpublicv1.CreateWorkFileShareLinkRequest{WorkId: work, Kind: agentpublicv1.WorkFileKind_WORK_FILE_KIND_ARTIFACT, Name: "answer.md"})
	denied(err)

	_, err = access.AdmitWorkReader(sessionControlRequest(m.ctx), &agentprivatev1.AdmitWorkReaderRequest{WorkId: work, ReaderId: strangerID})
	denied(err)
	_, err = files.ListWorkFiles(stranger.ctx, &agentpublicv1.ListWorkFilesRequest{WorkId: work})
	denied(err)

	recovery := application.NewRecovery(m.app, &queuedTasks{}, nil, nil, nil, nil, nil)
	payload := &accountintegrationv1.ApplicationRemovedV1{EventId: uuid.NewString(), ApplicationId: supportDesk}
	fact, err := integrationmessage.New(integrationmessage.KindOf(payload), payload, integrationmessage.WithKey(supportDesk))
	require.NoError(t, err)
	require.NoError(t, messagehandler.NewApplicationRemovalHandler(recovery).Handle(m.ctx, fact))
	_, err = access.AdmitWorkReader(sessionControlRequest(m.ctx), &agentprivatev1.AdmitWorkReaderRequest{WorkId: work, ReaderId: supportDesk})
	denied(err)
	readable()
}
