//go:build integration

package agent_test

import (
	"context"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	agentprivatev1 "github.com/talgent-work/contracts/gen/go/agent/private/v1"
	"github.com/talgent-work/contracts/gen/go/agent/private/v1/agentprivatev1connect"
	agentpublicv1 "github.com/talgent-work/contracts/gen/go/agent/public/v1"
	agentpublicv2 "github.com/talgent-work/contracts/gen/go/agent/public/v2"
	"github.com/talgent-work/contracts/gen/go/agent/public/v2/agentpublicv2connect"

	"github.com/talgent-work/workbench/internal/pkg/authn"
)

// callerWorks are one person's Standalone Work, an Application's Work and an
// Extension Work started under the person's Agent in the name of a Project.
type callerWorks struct {
	person, standalone, application, extension string
}

const readersProject = "project-readers"

func startCallerWorks(t *testing.T, m *lifecycleModule) callerWorks {
	t.Helper()
	works := callerWorks{person: uuid.NewString()}
	var err error
	works.standalone, err = m.as("user", works.person).start(t, uuid.NewString(), "Draft my plan")
	require.NoError(t, err)
	works.application, err = m.as("application", supportDesk).start(t, uuid.NewString(), "Answer ticket 42")
	require.NoError(t, err)
	own, err := m.client.GetWork(m.control(), &agentprivatev1.GetWorkRequest{SubjectId: works.person, WorkId: works.standalone})
	require.NoError(t, err)
	created, err := m.client.CreateWork(m.control(), &agentprivatev1.CreateWorkRequest{RequestId: uuid.NewString(), SubjectId: works.person, StartingUserId: works.person, AgentId: own.GetAgentId(),
		WorkCaller: &agentpublicv1.WorkCaller{Kind: "project", Id: readersProject}, WorkKey: &agentpublicv1.WorkKey{Kind: "case", Id: uuid.NewString()}})
	require.NoError(t, err)
	works.extension = created.GetWorkId()
	return works
}

func (m *lifecycleModule) workAccess() agentprivatev1connect.WorkAccessServiceClient {
	return agentprivatev1connect.NewWorkAccessServiceClient(connect.NewClient(connecthttp.NewTransport(m.public, m.url, connecthttp.WithReadMaxBytes(0))))
}

func (m *lifecycleModule) workFacts() agentprivatev1connect.WorkFactsServiceClient {
	return agentprivatev1connect.NewWorkFactsServiceClient(connect.NewClient(connecthttp.NewTransport(m.public, m.url, connecthttp.WithReadMaxBytes(0))))
}

// asAgentOf reaches the public Work API as the Agent of a Work, through the
// identity headers the entry writes from its Identity Token.
func (m *lifecycleModule) asAgentOf(workID, workCaller string) (context.Context, agentpublicv2connect.WorkServiceClient) {
	ctx, info := connect.NewClientContext(m.ctx)
	info.RequestHeader().Set(authn.HeaderAuthenticatedBy, "envoy")
	info.RequestHeader().Set(authn.HeaderPrincipal, "agent")
	info.RequestHeader().Set(authn.HeaderSubject, workID)
	info.RequestHeader().Set(authn.HeaderAgentID, "agent-definition")
	info.RequestHeader().Set(authn.HeaderPurpose, "work")
	info.RequestHeader().Set(authn.HeaderWorkCaller, workCaller)
	return ctx, agentpublicv2connect.NewWorkServiceClient(connect.NewClient(connecthttp.NewTransport(m.public, m.url, connecthttp.WithReadMaxBytes(0))))
}

// An extension acts for the owner only on a Work in the name of a kind it
// defines: on a person's or an Application's Standalone Work the private API
// refuses input and Work Share grants, which only the owner gives.
func TestDelegatedControlReachesOnlyExtensionWorks(t *testing.T) {
	m := newLifecycleModule(t).start()
	works := startCallerWorks(t, m)
	member := uuid.NewString()

	for _, standalone := range []string{works.standalone, works.application} {
		_, err := m.client.DeliverInput(m.control(), &agentprivatev1.DeliverInputRequest{SubjectId: works.person, WorkId: standalone, InputId: uuid.NewString(), Input: "Go on"})
		require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err), "input to %s", standalone)
		_, err = m.workAccess().GrantWorkShare(m.control(), &agentprivatev1.GrantWorkShareRequest{RequesterId: works.person, WorkId: standalone, ReaderId: member,
			Admission: &agentprivatev1.ReaderAdmission{WorkCaller: &agentpublicv1.WorkCaller{Kind: "project", Id: readersProject}}})
		require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err), "Work Share on %s", standalone)
	}

	_, err := m.client.DeliverInput(m.control(), &agentprivatev1.DeliverInputRequest{SubjectId: works.person, WorkId: works.extension, InputId: uuid.NewString(), Input: "Go on"})
	require.NoError(t, err)
	_, err = m.workAccess().GrantWorkShare(m.control(), &agentprivatev1.GrantWorkShareRequest{RequesterId: works.person, WorkId: works.extension, ReaderId: member,
		Admission: &agentprivatev1.ReaderAdmission{WorkCaller: &agentpublicv1.WorkCaller{Kind: "project", Id: readersProject}}})
	require.NoError(t, err, "a member of the Work's Project")
	_, err = m.workAccess().GrantWorkShare(m.control(), &agentprivatev1.GrantWorkShareRequest{RequesterId: works.person, WorkId: works.extension, ReaderId: uuid.NewString(),
		Admission: &agentprivatev1.ReaderAdmission{WorkCaller: &agentpublicv1.WorkCaller{Kind: "project", Id: "another-project"}}})
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err), "a member of another Project")
}

// An extension lists the Works it started; a person's and an Application's
// Standalone Works never appear in its listing, while the owners list them
// through the public API.
func TestTheExtensionListingHoldsOnlyExtensionWorks(t *testing.T) {
	m := newLifecycleModule(t).start()
	works := startCallerWorks(t, m)

	listed, err := m.workFacts().ListWorks(m.control(), &agentprivatev1.ListWorksRequest{AgentOwner: works.person})
	require.NoError(t, err)
	var ids []string
	for _, work := range listed.GetWorks() {
		ids = append(ids, work.GetWorkId())
		require.Equal(t, "project", work.GetWorkCaller().GetKind())
	}
	require.Equal(t, []string{works.extension}, ids)

	require.Equal(t, []string{works.standalone}, m.as("user", works.person).listed(t), "the person's own list holds their Standalone Work only")
	require.Equal(t, []string{works.application}, m.as("application", supportDesk).listed(t))
}

// The Agent of a Work whose Work Caller is a person reads that person's
// Personal Library; the Agent of the same person's Work in the name of a
// Project, and of an Application's Work, read no one's, whatever their
// identity claims.
func TestOnlyTheAgentOfAPersonsWorkReadsTheirLibrary(t *testing.T) {
	m := newLifecycleModule(t).start()
	works := startCallerWorks(t, m)

	ctx, client := m.asAgentOf(works.standalone, "person:"+works.person)
	_, err := client.ListLibraryFiles(ctx, &agentpublicv2.ListLibraryFilesRequest{})
	require.NoError(t, err)

	ctx, client = m.asAgentOf(works.extension, "person:"+works.person)
	_, err = client.ListLibraryFiles(ctx, &agentpublicv2.ListLibraryFilesRequest{})
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err), "the Work's own Work Caller decides, not the claim")

	ctx, client = m.asAgentOf(works.application, "application:"+supportDesk)
	_, err = client.ListLibraryFiles(ctx, &agentpublicv2.ListLibraryFilesRequest{})
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
}

// A person's Agent's owner switches its Work in the name of a Project between
// private and public; a Standalone Work stays private.
func TestOnlyAnExtensionWorkBecomesPublic(t *testing.T) {
	m := newLifecycleModule(t).start()
	works := startCallerWorks(t, m)
	owner := m.as("user", works.person)

	_, err := owner.work.ChangeWorkVisibility(owner.ctx, &agentpublicv1.ChangeWorkVisibilityRequest{WorkId: works.standalone, Public: true})
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
	changed, err := owner.work.ChangeWorkVisibility(owner.ctx, &agentpublicv1.ChangeWorkVisibilityRequest{WorkId: works.extension, Public: true})
	require.NoError(t, err)
	require.True(t, changed.GetWork().GetVisibilityPublic())
}
