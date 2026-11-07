package connectrpc_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	domainevent "github.com/go-jimu/components/ddd/event"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	agentprivatev1 "github.com/talgent-work/contracts/gen/go/agent/private/v1"
	"github.com/talgent-work/contracts/gen/go/agent/private/v1/agentprivatev1connect"
	agentpublicv1 "github.com/talgent-work/contracts/gen/go/agent/public/v1"
	"github.com/talgent-work/contracts/gen/go/agent/public/v1/agentpublicv1connect"
	agentmodule "github.com/talgent-work/workbench/internal/business/agent"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/eventhandler"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/pkg/authn"
	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"
)

// accessFixture serves Agent's handlers over in-memory Works: a human's Work
// and a project-scoped Agent's Work, both bound to project-a.
type accessFixture struct {
	sessions    *rpcSessions
	public      agentpublicv1connect.AgentWorkServiceClient
	access      agentprivatev1connect.WorkAccessServiceClient
	workspaces  agentprivatev1connect.WorkspaceReadingServiceClient
	agent       agentprivatev1connect.AgentServiceClient
	withdrawals *recordedWithdrawals
	humanWork   string
	digitalWork string
}

func newAccessFixture(t *testing.T) *accessFixture {
	t.Helper()
	return newAccessFixtureWith(t, nil)
}

// newAccessFixtureWith also serves workspaces through views, Storage's stand-in.
func newAccessFixtureWith(t *testing.T, views application.WorkspaceViewPort) *accessFixture {
	t.Helper()
	agents := &rpcAgents{items: map[string]*domain.AgentDefinition{}}
	dispatcher := domainevent.NewDispatcher(domainevent.WithDelayClose(0))
	t.Cleanup(func() { _ = dispatcher.Close(context.Background()) })
	sessions := &rpcSessions{items: map[string]*domain.AgentWork{}, events: dispatcher}
	employee := accessDefinition(t, agents, domain.UsageScope{Kind: "employee", Reference: "owner"}, domain.VisibilityRule{})
	coordinator := accessDefinition(t, agents, domain.UsageScope{Kind: "project", Reference: "project-a"}, domain.VisibilityRule{Public: true})
	f := &accessFixture{sessions: sessions, withdrawals: &recordedWithdrawals{}}
	f.humanWork = accessWork(t, sessions, employee, "owner")
	f.digitalWork = accessWork(t, sessions, coordinator, "owner")

	dispatcher.Subscribe(eventhandler.NewWorkAccessWithdrawals(f.withdrawals, slog.Default()))
	requests := &memoryCreations{items: map[string]command.WorkCreationRequest{}}
	app := &application.Application{Definitions: agents, Works: sessions, WorkPrincipals: ownerAuthorization{sessions}, WorkViews: sessionViews{sessions}, Logger: slog.Default(),
		Commands:       application.Commands{CreateRequestedWork: command.NewCreateRequestedWorkHandler(requests, nil, nil)},
		Queries:        application.Queries{GetWorkView: query.NewGetWorkViewHandler(ownerAuthorization{sessions}, sessionViews{sessions})},
		WorkspaceViews: views,
	}
	mux := http.NewServeMux()
	agentmodule.RegisterRPCHandlers(mux, app, agentmodule.RPCConfig{ControlToken: "control", FeedbackToken: "feedback"}, slog.Default(), nil)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	f.public = agentpublicv1connect.NewAgentWorkServiceClient(connect.NewClient(connecthttp.NewTransport(server.Client(), server.URL, connecthttp.WithReadMaxBytes(0))))
	f.access = agentprivatev1connect.NewWorkAccessServiceClient(connect.NewClient(connecthttp.NewTransport(server.Client(), server.URL, connecthttp.WithReadMaxBytes(0))))
	f.workspaces = agentprivatev1connect.NewWorkspaceReadingServiceClient(connect.NewClient(connecthttp.NewTransport(server.Client(), server.URL, connecthttp.WithReadMaxBytes(0))))
	f.agent = agentprivatev1connect.NewAgentServiceClient(connect.NewClient(connecthttp.NewTransport(server.Client(), server.URL, connecthttp.WithReadMaxBytes(0))))
	return f
}

func accessDefinition(t *testing.T, agents *rpcAgents, scope domain.UsageScope, rule domain.VisibilityRule) *domain.AgentDefinition {
	t.Helper()
	definition, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"owner"}, Purpose: "work", UsageScope: scope, HomeBinding: domain.HomeBinding{OwnerID: scope.Reference, Namespace: "personal"}, Visibility: rule})
	require.NoError(t, err)
	require.NoError(t, agents.Save(context.Background(), definition))
	return definition
}

func accessWork(t *testing.T, sessions *rpcSessions, definition *domain.AgentDefinition, owner string) string {
	t.Helper()
	work, err := domain.NewAgentWork(context.Background(), domain.WorkInput{Subject: domain.WorkSubject{Kind: "case", ID: uuid.NewString()}, ProjectID: "project-a", WorkModel: domain.WorkModelSelection{ConfigurationID: "default"}, StartedBy: owner, ID: uuid.NewString(), AgentID: definition.ID, Visibility: definition.Visibility,
		Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}}, WorkspaceReference: "workspace", HomeBinding: definition.HomeBinding,
	}, domain.StartAdmission{AdmittedAt: time.Now(), AgentID: definition.ID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", DeskID: "desk-test"}, ModelConfigurationProvided: true}, &rpcExecution{})
	require.NoError(t, err)
	sessions.items[work.ID] = work
	return work.ID
}

// asUser is the context of a call the gateway delegates for user.
func asUser(ctx context.Context, user string) context.Context {
	ctx, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set(authn.HeaderServiceID, "gateway-test")
	info.RequestHeader().Set(authn.HeaderDelegatedUserID, user)
	return ctx
}

func (f *accessFixture) admit(t *testing.T, work, reader string, admission *agentprivatev1.ReaderAdmission) (agentprivatev1.WorkReadBasis, connect.Code) {
	t.Helper()
	response, err := f.access.AdmitWorkReader(authenticated(context.Background(), "control"), &agentprivatev1.AdmitWorkReaderRequest{WorkId: work, ReaderId: reader, Admission: admission})
	if err != nil {
		return 0, connect.CodeOf(err)
	}
	return response.GetBasis(), 0
}

var memberOfProjectA = &agentprivatev1.ReaderAdmission{Reference: "project", Value: "project-a"}

func TestWorkReadersFollowOwnerVisibilityAndWorkShares(t *testing.T) {
	ctx := context.Background()
	f := newAccessFixture(t)

	basis, _ := f.admit(t, f.humanWork, "owner", nil)
	require.Equal(t, agentprivatev1.WorkReadBasis_WORK_READ_BASIS_OWNER, basis)
	_, code := f.admit(t, f.humanWork, "member", memberOfProjectA)
	require.Equal(t, connect.CodePermissionDenied, code, "an employee's Work is private by default")

	_, err := f.access.GrantWorkShare(authenticated(ctx, "control"), &agentprivatev1.GrantWorkShareRequest{WorkId: f.humanWork, RequesterId: "owner", ReaderId: "reader", Admission: memberOfProjectA})
	require.NoError(t, err)
	basis, _ = f.admit(t, f.humanWork, "reader", nil)
	require.Equal(t, agentprivatev1.WorkReadBasis_WORK_READ_BASIS_SHARE, basis)

	_, err = f.access.GrantWorkShare(authenticated(ctx, "control"), &agentprivatev1.GrantWorkShareRequest{WorkId: f.humanWork, RequesterId: "owner", ReaderId: "outsider", Admission: &agentprivatev1.ReaderAdmission{Reference: "project", Value: "project-b"}})
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err), "a Work Share never crosses the Work's Project")
	_, err = f.access.GrantWorkShare(authenticated(ctx, "control"), &agentprivatev1.GrantWorkShareRequest{WorkId: f.humanWork, RequesterId: "reader", ReaderId: "member", Admission: memberOfProjectA})
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err), "only the owner shares")
	_, err = f.access.GrantWorkShare(ctx, &agentprivatev1.GrantWorkShareRequest{WorkId: f.humanWork, RequesterId: "owner", ReaderId: "member", Admission: memberOfProjectA})
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err), "only a trusted caller supplies membership")

	changed, err := f.public.ChangeWorkVisibility(asUser(ctx, "owner"), &agentpublicv1.ChangeWorkVisibilityRequest{WorkId: f.humanWork, Public: true})
	require.NoError(t, err)
	require.True(t, changed.GetWork().GetVisibilityPublic())
	require.Equal(t, []string{"reader"}, changed.GetWork().GetShareReaderIds())
	basis, _ = f.admit(t, f.humanWork, "member", memberOfProjectA)
	require.Equal(t, agentprivatev1.WorkReadBasis_WORK_READ_BASIS_PUBLIC, basis)
	_, code = f.admit(t, f.humanWork, "member", nil)
	require.Equal(t, connect.CodePermissionDenied, code, "public reading needs the trusted caller's admission")
	_, code = f.admit(t, f.humanWork, "member", &agentprivatev1.ReaderAdmission{Reference: "project", Value: "project-b"})
	require.Equal(t, connect.CodePermissionDenied, code, "visibility never crosses a Project")
	_, code = f.admit(t, uuid.NewString(), "owner", nil)
	require.Equal(t, connect.CodePermissionDenied, code, "an unknown Work admits no one")
}

// A caller starting the reader's stream gets the Work's current state with the
// admission, SubAgents included; a refused reader gets nothing.
func TestAnAdmittedReaderStartsFromTheWorksCurrentState(t *testing.T) {
	ctx := context.Background()
	f := newAccessFixture(t)
	require.NoError(t, f.sessions.items[f.digitalWork].RecordSubAgents(10, []domain.SubAgent{{ID: "a", Type: "Explore", Task: "map the code", Status: domain.SubAgentRunning}}))
	response, err := f.access.AdmitWorkReader(authenticated(ctx, "control"), &agentprivatev1.AdmitWorkReaderRequest{WorkId: f.digitalWork, ReaderId: "member", Admission: memberOfProjectA, IncludeWorkState: true})
	require.NoError(t, err)
	state := response.GetWorkState()
	require.Equal(t, f.digitalWork, state.GetAgentWorkId())
	require.Equal(t, "starting", state.GetState())
	require.Len(t, state.GetSubagents(), 1)
	require.Equal(t, "map the code", state.GetSubagents()[0].GetTask())
	require.Equal(t, "running", state.GetSubagents()[0].GetStatus())

	response, err = f.access.AdmitWorkReader(authenticated(ctx, "control"), &agentprivatev1.AdmitWorkReaderRequest{WorkId: f.digitalWork, ReaderId: "member", Admission: memberOfProjectA})
	require.NoError(t, err)
	require.Nil(t, response.GetWorkState(), "only a caller asking for it reads the state")
	_, err = f.access.AdmitWorkReader(authenticated(ctx, "control"), &agentprivatev1.AdmitWorkReaderRequest{WorkId: f.humanWork, ReaderId: "member", Admission: memberOfProjectA, IncludeWorkState: true})
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
}

func TestOnlyTheOwnerOfAHumansWorkChangesItsAccess(t *testing.T) {
	ctx := context.Background()
	f := newAccessFixture(t)
	_, err := f.public.ChangeWorkVisibility(asUser(ctx, "member"), &agentpublicv1.ChangeWorkVisibilityRequest{WorkId: f.humanWork, Public: true})
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	_, err = f.public.RevokeWorkShare(asUser(ctx, "member"), &agentpublicv1.RevokeWorkShareRequest{WorkId: f.humanWork, ReaderUserId: "reader"})
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))

	_, err = f.public.ChangeWorkVisibility(asUser(ctx, "owner"), &agentpublicv1.ChangeWorkVisibilityRequest{WorkId: f.digitalWork, Public: false})
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err), "a project-scoped Agent's Work keeps its declared visibility")
	_, err = f.access.GrantWorkShare(authenticated(ctx, "control"), &agentprivatev1.GrantWorkShareRequest{WorkId: f.digitalWork, RequesterId: "owner", ReaderId: "member", Admission: memberOfProjectA})
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err), "a project-scoped Agent's Work has no Work Shares")
	basis, _ := f.admit(t, f.digitalWork, "member", memberOfProjectA)
	require.Equal(t, agentprivatev1.WorkReadBasis_WORK_READ_BASIS_PUBLIC, basis, "a project-scoped Agent's Work is public within its Project")
}

func TestRevokingAShareOrMakingTheWorkPrivateWithdrawsReadersImmediately(t *testing.T) {
	ctx := context.Background()
	f := newAccessFixture(t)
	for _, reader := range []string{"reader", "kept"} {
		_, err := f.access.GrantWorkShare(authenticated(ctx, "control"), &agentprivatev1.GrantWorkShareRequest{WorkId: f.humanWork, RequesterId: "owner", ReaderId: reader, Admission: memberOfProjectA})
		require.NoError(t, err)
	}
	revoked, err := f.public.RevokeWorkShare(asUser(ctx, "owner"), &agentpublicv1.RevokeWorkShareRequest{WorkId: f.humanWork, ReaderUserId: "reader"})
	require.NoError(t, err)
	require.Equal(t, []string{"kept"}, revoked.GetWork().GetShareReaderIds())
	_, code := f.admit(t, f.humanWork, "reader", nil)
	require.Equal(t, connect.CodePermissionDenied, code)
	require.Eventually(t, func() bool {
		return slices.ContainsFunc(f.withdrawals.all(), func(w eventhandler.WorkAccessWithdrawal) bool {
			return w.WorkID == f.humanWork && w.RevokedReaderID == "reader"
		})
	}, time.Second, 10*time.Millisecond)

	_, err = f.public.ChangeWorkVisibility(asUser(ctx, "owner"), &agentpublicv1.ChangeWorkVisibilityRequest{WorkId: f.humanWork, Public: true})
	require.NoError(t, err)
	_, err = f.public.ChangeWorkVisibility(asUser(ctx, "owner"), &agentpublicv1.ChangeWorkVisibilityRequest{WorkId: f.humanWork, Public: false})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return slices.ContainsFunc(f.withdrawals.all(), func(w eventhandler.WorkAccessWithdrawal) bool {
			return w.WorkID == f.humanWork && w.MadePrivate && slices.Equal(w.RetainedReaderIDs, []string{"owner", "kept"})
		})
	}, time.Second, 10*time.Millisecond, "readers other than the owner and Work Share holders lose access")
}

func TestOwnerOperationsRefuseOtherReaders(t *testing.T) {
	ctx := context.Background()
	f := newAccessFixture(t)
	_, err := f.access.GrantWorkShare(authenticated(ctx, "control"), &agentprivatev1.GrantWorkShareRequest{WorkId: f.humanWork, RequesterId: "owner", ReaderId: "reader", Admission: memberOfProjectA})
	require.NoError(t, err)
	_, err = f.public.GetWork(asUser(ctx, "reader"), &agentpublicv1.GetWorkRequest{WorkId: f.humanWork})
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err), "a Work Share holder reads the stream, not the owner's Work controls")
	owned, err := f.public.GetWork(asUser(ctx, "owner"), &agentpublicv1.GetWorkRequest{WorkId: f.humanWork})
	require.NoError(t, err)
	require.Equal(t, []string{"reader"}, owned.GetWork().GetShareReaderIds())
}

func TestCancelledCreationRequestIdentityNeverCreatesAWork(t *testing.T) {
	ctx := context.Background()
	f := newAccessFixture(t)
	requestID := uuid.NewString()
	outcome, err := f.agent.GetWorkCreation(authenticated(ctx, "control"), &agentprivatev1.GetWorkCreationRequest{RequestId: requestID})
	require.NoError(t, err)
	require.Equal(t, agentprivatev1.WorkCreationOutcome_WORK_CREATION_OUTCOME_UNKNOWN, outcome.GetOutcome())

	cancelled, err := f.agent.CancelWorkCreation(authenticated(ctx, "control"), &agentprivatev1.CancelWorkCreationRequest{RequestId: requestID})
	require.NoError(t, err)
	require.Empty(t, cancelled.GetWorkId())
	outcome, err = f.agent.GetWorkCreation(authenticated(ctx, "control"), &agentprivatev1.GetWorkCreationRequest{RequestId: requestID})
	require.NoError(t, err)
	require.Equal(t, agentprivatev1.WorkCreationOutcome_WORK_CREATION_OUTCOME_CANCELLED, outcome.GetOutcome())

	works := len(f.sessions.items)
	_, err = f.agent.PrepareWorkExecution(authenticated(ctx, "control"), &agentprivatev1.PrepareWorkExecutionRequest{SubjectId: "owner", RequestId: requestID, WorkBranch: "talgent/" + "TAL-1", InputId: uuid.NewString(), Input: "Start",
		Creation: &agentprivatev1.CreateWorkRequest{SubjectId: "owner", AgentId: uuid.NewString(), WorkspaceReference: "workspace", Subject: &agentprivatev1.WorkSubject{Kind: "case", Id: "case"}, ProjectId: "project-a"}})
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err), "a delayed request carrying a cancelled identity is refused")
	require.Len(t, f.sessions.items, works, "and creates nothing")
}

type recordedWithdrawals struct {
	mu    sync.Mutex
	items []eventhandler.WorkAccessWithdrawal
}

func (r *recordedWithdrawals) PublishWorkAccessWithdrawal(_ context.Context, withdrawal eventhandler.WorkAccessWithdrawal) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items = append(r.items, withdrawal)
	return nil
}

func (r *recordedWithdrawals) all() []eventhandler.WorkAccessWithdrawal {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.items)
}

type ownerAuthorization struct{ sessions *rpcSessions }

func (a ownerAuthorization) AuthorizeWork(_ context.Context, subject, id string) error {
	work, ok := a.sessions.items[id]
	if !ok || subject == "" || work.StartedBy != subject {
		return access.ErrAccessDenied
	}
	return nil
}

type sessionViews struct{ sessions *rpcSessions }

func (v sessionViews) GetWorkView(_ context.Context, id string) (query.WorkView, error) {
	work, ok := v.sessions.items[id]
	if !ok {
		return query.WorkView{}, domain.ErrWorkNotFound
	}
	view := query.WorkView{WorkID: work.ID, AgentOwner: work.StartedBy, Subject: work.Subject, ProjectID: work.ProjectID, State: string(work.State), Visibility: work.Visibility, WorkShares: slices.Clone(work.WorkShares), Version: work.Version}
	for _, agent := range work.SubAgents() {
		view.SubAgents = append(view.SubAgents, query.SubAgent{ID: agent.ID, Type: agent.Type, Task: agent.Task, Status: string(agent.Status)})
	}
	return view, nil
}

// memoryCreations keeps creation-request records under one lock, as the
// repository serializes them.
type memoryCreations struct {
	mu    sync.Mutex
	items map[string]command.WorkCreationRequest
}

func (m *memoryCreations) GetWorkCreation(_ context.Context, id string) (command.WorkCreationRequest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	request, ok := m.items[id]
	if !ok {
		return command.WorkCreationRequest{}, command.ErrWorkCreationNotFound
	}
	return request, nil
}

func (m *memoryCreations) RetainWorkCreation(_ context.Context, request command.WorkCreationRequest) (command.WorkCreationRequest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if retained, ok := m.items[request.ID]; ok {
		return retained, nil
	}
	m.items[request.ID] = request
	return request, nil
}

func (m *memoryCreations) CancelWorkCreation(_ context.Context, id string) (command.WorkCreationRequest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	request := m.items[id]
	request.ID = id
	if request.WorkID == "" {
		request.Canceled = true
	}
	m.items[id] = request
	return request, nil
}

func (m *memoryCreations) ConfirmWorkCreation(_ context.Context, id, workID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	request := m.items[id]
	request.WorkID, request.BindingConfirmed = workID, true
	m.items[id] = request
	return nil
}

func (m *memoryCreations) CompleteWorkCreation(context.Context, string) error { return nil }
