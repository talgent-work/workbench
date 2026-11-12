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
	agentpublicv1 "github.com/talgent-work/contracts/gen/go/agent/public/v1"
	"github.com/talgent-work/contracts/gen/go/agent/public/v1/agentpublicv1connect"
	agentpublicv2 "github.com/talgent-work/contracts/gen/go/agent/public/v2"
	"github.com/talgent-work/contracts/gen/go/agent/public/v2/agentpublicv2connect"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/pkg/authn"
)

// ownerClients reach the public Work APIs as one owner, through the
// identity headers the entry writes.
type ownerClients struct {
	ctx   context.Context
	works agentpublicv2connect.WorkServiceClient
	work  agentpublicv1connect.AgentWorkServiceClient
}

func (m *lifecycleModule) as(principal, subject string) ownerClients {
	m.t.Helper()
	ctx, info := connect.NewClientContext(m.ctx)
	info.RequestHeader().Set(authn.HeaderAuthenticatedBy, "envoy")
	info.RequestHeader().Set(authn.HeaderPrincipal, principal)
	info.RequestHeader().Set(authn.HeaderSubject, subject)
	client := connect.NewClient(connecthttp.NewTransport(m.public, m.url, connecthttp.WithReadMaxBytes(0)))
	return ownerClients{ctx: ctx, works: agentpublicv2connect.NewWorkServiceClient(client), work: agentpublicv1connect.NewAgentWorkServiceClient(client)}
}

func (c ownerClients) start(t *testing.T, requestID, message string) (string, error) {
	t.Helper()
	response, err := c.works.StartWork(c.ctx, &agentpublicv2.StartWorkRequest{RequestId: requestID, InitialMessage: message})
	return response.GetWorkId(), err
}

func (c ownerClients) listed(t *testing.T) []string {
	t.Helper()
	response, err := c.works.ListWorks(c.ctx, &agentpublicv2.ListWorksRequest{})
	require.NoError(t, err)
	var ids []string
	for _, work := range response.GetWorks() {
		ids = append(ids, work.GetWorkId())
	}
	return ids
}

// An Application starts Standalone Works under the one definition Agent
// establishes for it on its first start, named after it and owned by it
// alone; a repeated request identity returns the Work it started. It lists
// and retitles only its own Works.
func TestAnApplicationStartsItsOwnWorksUnderItsOneAgent(t *testing.T) {
	m := newLifecycleModule(t).start()
	desk := m.as("application", supportDesk)

	request := uuid.NewString()
	first, err := desk.start(t, request, "Answer ticket 42\nwith the refund policy")
	require.NoError(t, err)
	again, err := desk.start(t, request, "Answer ticket 42\nwith the refund policy")
	require.NoError(t, err)
	require.Equal(t, first, again, "a retried start never starts a second Work")
	second, err := desk.start(t, uuid.NewString(), "Answer ticket 43")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{first, second}, desk.listed(t))

	one, err := m.app.Works.Get(m.ctx, first)
	require.NoError(t, err)
	other, err := m.app.Works.Get(m.ctx, second)
	require.NoError(t, err)
	require.Equal(t, one.AgentID, other.AgentID, "all its Works share its one Agent")
	require.True(t, one.Standalone())
	require.Empty(t, one.RepresentedPerson, "an Application's Work represents no person")
	require.Equal(t, supportDesk, one.StartedBy, "the Application starts it as itself")
	definition, err := m.app.Definitions.Get(m.ctx, one.AgentID)
	require.NoError(t, err)
	require.Equal(t, supportDesk, definition.OwningApplication())
	require.Equal(t, []string{supportDesk}, definition.Owners, "its Application Owner is no Agent Owner")
	require.Equal(t, "Support Desk", definition.AgentName)
	require.Equal(t, domain.StandaloneWorkPurpose, definition.Purpose)

	_, err = desk.works.ChangeWorkTitle(desk.ctx, &agentpublicv2.ChangeWorkTitleRequest{WorkId: first, Title: "Ticket 42"})
	require.NoError(t, err)
	view, err := desk.work.GetWork(desk.ctx, &agentpublicv1.GetWorkRequest{WorkId: first})
	require.NoError(t, err)
	require.Equal(t, first, view.GetWork().GetWorkId())

	options, err := desk.works.GetWorkStartOptions(desk.ctx, &agentpublicv2.GetWorkStartOptionsRequest{})
	require.NoError(t, err)
	require.Equal(t, definition.WorkDefaults.StartupAgent, options.GetOptions().GetDefaultStartupAgent(), "start options come from its own Agent")
}

// A request identity is its caller's own: two owners that happen to choose
// the same one each start their own Work, on their own Desk.
func TestEqualRequestIdentitiesOfTwoOwnersStartTwoWorks(t *testing.T) {
	m := newLifecycleModule(t).start()
	desk, billing := m.as("application", supportDesk), m.as("application", billingBot)
	person := m.as("user", uuid.NewString())

	request := uuid.NewString()
	works := map[string]bool{}
	for _, owner := range []ownerClients{desk, billing, person} {
		work, err := owner.start(t, request, "Answer ticket 42")
		require.NoError(t, err)
		works[work] = true
		require.Equal(t, []string{work}, owner.listed(t))
	}
	require.Len(t, works, 3)
}

// An Application drives its own Work, and only its own: another Application
// and a person reach none of it, and the Application reaches no person's
// Work. Procedures that are a person's alone refuse it.
func TestAnApplicationReachesOnlyItsOwnWorks(t *testing.T) {
	m := newLifecycleModule(t).start()
	desk, billing := m.as("application", supportDesk), m.as("application", billingBot)
	person := m.as("user", uuid.NewString())

	work, err := desk.start(t, uuid.NewString(), "Answer ticket 42")
	require.NoError(t, err)
	personal, err := person.start(t, uuid.NewString(), "Draft my plan")
	require.NoError(t, err)

	require.Empty(t, billing.listed(t), "another Application lists none of its Works")
	require.Equal(t, []string{personal}, person.listed(t), "a person lists only their own")
	require.Equal(t, []string{work}, desk.listed(t))

	denied := func(err error) { t.Helper(); require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err)) }
	for _, other := range []ownerClients{billing, person} {
		_, err = other.work.GetWork(other.ctx, &agentpublicv1.GetWorkRequest{WorkId: work})
		denied(err)
		_, err = other.work.SendMessage(other.ctx, &agentpublicv1.SendMessageRequest{WorkId: work, InputId: uuid.NewString(), Text: "hijack"})
		denied(err)
		_, err = other.work.StopWork(other.ctx, &agentpublicv1.StopWorkRequest{WorkId: work})
		denied(err)
		_, err = other.work.ArchiveWork(other.ctx, &agentpublicv1.ArchiveWorkRequest{WorkId: work})
		denied(err)
		_, err = other.works.ChangeWorkTitle(other.ctx, &agentpublicv2.ChangeWorkTitleRequest{WorkId: work, Title: "Mine"})
		denied(err)
	}
	_, err = desk.work.GetWork(desk.ctx, &agentpublicv1.GetWorkRequest{WorkId: personal})
	denied(err)
	_, err = desk.work.SendMessage(desk.ctx, &agentpublicv1.SendMessageRequest{WorkId: personal, InputId: uuid.NewString(), Text: "hijack"})
	denied(err)

	_, err = desk.works.GrantWorkShare(desk.ctx, &agentpublicv2.GrantWorkShareRequest{WorkId: work, ReaderUserId: uuid.NewString()})
	denied(err)
	_, err = desk.work.ChangeWorkVisibility(desk.ctx, &agentpublicv1.ChangeWorkVisibilityRequest{WorkId: work, Public: true})
	denied(err)
	_, err = desk.works.ListLibraryFiles(desk.ctx, &agentpublicv2.ListLibraryFilesRequest{})
	denied(err)
	_, err = desk.works.ListWorkStartRepositories(desk.ctx, &agentpublicv2.ListWorkStartRepositoriesRequest{})
	denied(err)
}

// An Application drives its Work as its owner: its runs' Identity Token
// states the Application, it sends further input and reads what waits for
// an answer, and it closes the Work once that input settled, which stops it
// first.
func TestAnApplicationDrivesAndClosesItsWork(t *testing.T) {
	m := newLifecycleModule(t).start()
	desk := m.as("application", supportDesk)
	first := uuid.NewString()
	work, err := desk.start(t, first, "Answer ticket 42")
	require.NoError(t, err)
	m.runs.ready(t, m.ctx, work)
	launch := m.runs.latest(t, m.ctx, work).Launch
	require.Equal(t, supportDesk, launch.OwningApplication, "the Work's Identity Token states its Application")
	require.Empty(t, launch.RepresentedPerson)

	input := uuid.NewString()
	_, err = desk.work.SendMessage(desk.ctx, &agentpublicv1.SendMessageRequest{WorkId: work, InputId: input, Text: "Also mention the deadline"})
	require.NoError(t, err)
	_, err = desk.work.ListPendingInteractions(desk.ctx, &agentpublicv1.ListPendingInteractionsRequest{WorkId: work})
	require.NoError(t, err)
	for _, settled := range []string{first, input} {
		m.port.settle(settled)
		require.NoError(t, m.report(&agentprivatev1.ReportRequest{ProcessId: launchedProcess, WorkId: work, Kind: agentprivatev1.FeedbackKind_FEEDBACK_KIND_INPUT_SETTLED, InputId: settled, InputOutcome: agentprivatev1.InputOutcome_INPUT_OUTCOME_COMPLETED}))
	}
	require.NoError(t, m.report(&agentprivatev1.ReportRequest{ProcessId: launchedProcess, WorkId: work, Kind: agentprivatev1.FeedbackKind_FEEDBACK_KIND_TURN_FINISHED, ObservationSequence: 20, TurnCompleted: true}))

	closing, err := desk.work.ArchiveWork(desk.ctx, &agentpublicv1.ArchiveWorkRequest{WorkId: work})
	require.NoError(t, err, "its owner closes it")
	require.Equal(t, work, closing.GetWork().GetWorkId())
	require.Equal(t, "stopping", closing.GetWork().GetState(), "a running Work is stopped before it closes")
}

// A removed Application, or one Account does not know, starts nothing.
func TestARemovedApplicationStartsNothing(t *testing.T) {
	m := newLifecycleModule(t).start()
	for _, application := range []string{retiredReporting, uuid.NewString()} {
		_, err := m.as("application", application).start(t, uuid.NewString(), "Report")
		require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err), application)
	}
}
