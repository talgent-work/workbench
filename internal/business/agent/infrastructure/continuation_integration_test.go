//go:build integration

package infrastructure_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	agentquery "github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/application/query/querytest"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	agentprivatev1 "github.com/talgent-work/contracts/gen/go/agent/private/v1"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"github.com/talgent-work/workbench/internal/testsupport/computertest"
	"xorm.io/xorm"
)

type continuationCapacity struct {
	err   error
	calls int
}

func (c *continuationCapacity) AdmitRoom(context.Context) error { c.calls++; return c.err }

func stoppedContinuationSession(t *testing.T) (workCreationFixture, string) {
	t.Helper()
	f := newWorkCreationFixture(t)
	ctx := context.Background()
	create := command.NewCreateWorkHandler(time.Now, f.agents, f.sessions, f.sessions, access.NewWorkAuthorizer(infrastructure.NewWorkAccessReader(f.db)), agentquery.NewModelConfigurations(agentquery.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "deployment-default"}}), commandtest.StartCatalog(&creationCatalog{}, creationConnections{}), computertest.Checked(startupAuthority{}), executionPort{}, creationPreparation(f.db, executionPort{}, agentquery.NewModelConfigurations(agentquery.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "deployment-default"}})))
	id, err := create.Handle(ctx, f.request)
	require.NoError(t, err)
	// The Work recovery its creation wakes hands off its initial task input.
	require.NoError(t, command.NewInputAcceptance(f.sessions, &commandtest.InputBuffer{}).HandOffInitialTaskInput(ctx, id))
	factory := executionPort{}
	ref := id
	feedback := command.NewApplyExecutionFeedbackHandler(time.Now, f.sessions, exitedProcess, factory, &commandtest.InputBuffer{}, &commandtest.InputSettlements{})
	for _, kind := range []command.ExecutionFeedbackKind{command.ExecutorPrepared, command.AgentStarted} {
		require.NoError(t, feedback.Handle(ctx, command.ExecutionFeedback{ProcessID: "process", Kind: kind, WorkID: ref}))
	}
	// A normal exit follows an admitted stop (Lifecycle observation).
	require.NoError(t, command.NewStopWorkHandler(f.sessions, access.NewWorkAuthorizer(infrastructure.NewWorkAccessReader(f.db)), factory).Handle(ctx, command.WorkControl{SubjectID: f.request.SubjectID, WorkID: id, RequestID: uuid.NewString()}))
	for _, kind := range []command.ExecutionFeedbackKind{command.ExecutionExited, command.ComputeReclaimed} {
		require.NoError(t, feedback.Handle(ctx, command.ExecutionFeedback{ProcessID: "process", Kind: kind, WorkID: ref}))
	}
	s, err := f.sessions.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, domain.WorkStopped, s.State)
	return f, id
}

func TestContinuationCapacityRejectionNeverReentersAdmissionWithoutExplicitRetry(t *testing.T) {
	f, id := stoppedContinuationSession(t)
	ctx := context.Background()
	before, err := f.sessions.Get(ctx, id)
	require.NoError(t, err)
	capacity := &continuationCapacity{err: domain.ErrCapacityUnavailable}
	starts := []domain.StartExecutionRequest{}
	factory := startupExecutionPort{onStart: func(r domain.StartExecutionRequest) error { starts = append(starts, r); return nil }}
	receipts := infrastructure.NewContinuationRequests(f.db, time.Now)
	handler := command.NewResumeWorkHandler(time.Now, f.sessions, f.agents, access.NewWorkAuthorizer(infrastructure.NewWorkAccessReader(f.db)), computertest.Checked(capacity), factory, receipts, querytest.ProvidedModels(), commandtest.UnchangedPlugins{})
	request := command.StartWork{SubjectID: "owner", WorkID: id, RequestID: uuid.NewString()}
	foreign := request
	foreign.SubjectID = "another-owner"
	err = handler.Handle(ctx, foreign)
	require.Error(t, err)
	_, found, err := receipts.GetContinuation(ctx, id)
	require.NoError(t, err)
	require.False(t, found, "unauthorized requests cannot retain a continuation")
	require.Zero(t, capacity.calls)
	err = handler.Handle(ctx, request)
	require.ErrorIs(t, err, domain.ErrCapacityUnavailable)
	pending, err := receipts.GetWorkContinuation(ctx, id)
	require.NoError(t, err)
	require.Equal(t, request.RequestID, pending.RequestID)
	require.False(t, pending.Pending)
	capacity.err = nil
	require.NoError(t, handler.Recover(ctx, id))
	err = handler.Handle(ctx, request)
	require.ErrorIs(t, err, domain.ErrCapacityUnavailable)
	require.Equal(t, 1, capacity.calls, "observation and replay cannot resubmit the rejected request")
	require.Empty(t, starts)
	retained, err := f.sessions.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, domain.WorkStopped, retained.State)
	require.Equal(t, before.HarnessProcess, retained.HarnessProcess)
	require.Equal(t, before.HarnessSessionReference, retained.HarnessSessionReference)
	request.RequestID = uuid.NewString()
	require.NoError(t, handler.Handle(ctx, request))
	admitted, found := admittedStart(t, f.sessions, request.RequestID)
	require.True(t, found)
	require.Equal(t, agentquery.AdmittedStart{WorkID: id, Kind: agentquery.StartKindResume}, admitted)
	require.Len(t, starts, 1)
	require.Equal(t, domain.ContextContinue, starts[0].ContextMode)
	require.Equal(t, before.HarnessSessionReference, starts[0].HarnessSessionReference)
	require.Equal(t, before.WorkspaceReference, starts[0].WorkspaceReference)
	require.Equal(t, 2, capacity.calls)
	retained, err = f.sessions.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, domain.WorkStarting, retained.State)
	require.Equal(t, before.CapabilitySnapshot, retained.CapabilitySnapshot)
}

type lostContinuationReceipt struct {
	command.ContinuationRequests
	retain, reject bool
}

var errContinuationAckLost = errors.New("continuation receipt acknowledgement lost")

func (r *lostContinuationReceipt) RetainContinuation(ctx context.Context, input command.ContinuationRequest, version int) (command.ContinuationRequest, error) {
	value, err := r.ContinuationRequests.RetainContinuation(ctx, input, version)
	if err == nil && r.retain {
		r.retain = false
		return command.ContinuationRequest{}, errContinuationAckLost
	}
	return value, err
}
func (r *lostContinuationReceipt) RecordContinuationRejection(ctx context.Context, id, requestID, reason string) error {
	err := r.ContinuationRequests.RecordContinuationRejection(ctx, id, requestID, reason)
	if err == nil && r.reject {
		r.reject = false
		return errContinuationAckLost
	}
	return err
}

func TestContinuationRecoversRejectionWithoutRequestingCapacity(t *testing.T) {
	f, id := stoppedContinuationSession(t)
	ctx := context.Background()
	receipts := infrastructure.NewContinuationRequests(f.db, time.Now)
	capacity := &continuationCapacity{err: domain.ErrCapacityUnavailable}
	factory := executionPort{}
	lost := &lostContinuationReceipt{ContinuationRequests: receipts, reject: true}
	h := command.NewResumeWorkHandler(time.Now, f.sessions, f.agents, access.NewWorkAuthorizer(infrastructure.NewWorkAccessReader(f.db)), computertest.Checked(capacity), factory, lost, querytest.ProvidedModels(), commandtest.UnchangedPlugins{})
	request := command.StartWork{SubjectID: "owner", WorkID: id, RequestID: uuid.NewString()}
	err := h.Handle(ctx, request)
	require.ErrorIs(t, err, errContinuationAckLost)
	view, err := receipts.GetWorkContinuation(ctx, id)
	require.NoError(t, err)
	require.True(t, view.Pending)
	other, err := xorm.NewEngine("mysql", f.dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = other.Close() })
	sessions := infrastructure.NewAgentWorkRepository(other, time.Now)
	capacity.err = nil
	rebuilt := command.NewResumeWorkHandler(time.Now, sessions, infrastructure.NewAgentDefinitionRepository(other, time.Now), access.NewWorkAuthorizer(infrastructure.NewWorkAccessReader(other)), computertest.Checked(capacity), factory, infrastructure.NewContinuationRequests(other, time.Now), querytest.ProvidedModels(), commandtest.UnchangedPlugins{})
	require.NoError(t, rebuilt.Recover(ctx, id))
	require.Equal(t, 1, capacity.calls)
	view, err = receipts.GetWorkContinuation(ctx, id)
	require.NoError(t, err)
	require.False(t, view.Pending)
	// A worker already holding admission context cannot commit after rejection
	// has been reconciled: the start request record was canceled.
	session, err := sessions.Get(ctx, id)
	require.NoError(t, err)
	require.NoError(t, session.Resume(ctx, domain.StartAdmission{AdmittedAt: time.Now(), AgentID: session.AgentID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", DeskID: "desk-test"}, ModelConfigurationProvided: true}, commandtest.Hold(factory)))
	require.ErrorIs(t, sessions.CommitStart(ctx, session, agentquery.StartRequest{ID: request.RequestID, Kind: agentquery.StartKindResume, WorkID: id}), command.ErrStartRequestCanceled)
	actual, err := sessions.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, domain.WorkStopped, actual.State)
}

type lostContinuationAdmission struct {
	*infrastructure.AgentWorkRepository
	lost bool
}

func (r *lostContinuationAdmission) CommitStart(ctx context.Context, s *domain.AgentWork, request agentquery.StartRequest) error {
	err := r.AgentWorkRepository.CommitStart(ctx, s, request)
	if err == nil && r.lost {
		r.lost = false
		return errContinuationAckLost
	}
	return err
}

func TestContinuationObservationResolvesAcceptedAdmissionAfterLostResponse(t *testing.T) {
	f, id := stoppedContinuationSession(t)
	ctx := context.Background()
	capacity := &continuationCapacity{}
	starts := []domain.StartExecutionRequest{}
	factory := startupExecutionPort{onStart: func(r domain.StartExecutionRequest) error { starts = append(starts, r); return nil }}
	receipts := infrastructure.NewContinuationRequests(f.db, time.Now)
	h := command.NewResumeWorkHandler(time.Now, &lostContinuationAdmission{AgentWorkRepository: f.sessions, lost: true}, f.agents, access.NewWorkAuthorizer(infrastructure.NewWorkAccessReader(f.db)), computertest.Checked(capacity), factory, receipts, querytest.ProvidedModels(), commandtest.UnchangedPlugins{})
	request := command.StartWork{SubjectID: "owner", WorkID: id, RequestID: uuid.NewString()}
	err := h.Handle(ctx, request)
	require.ErrorIs(t, err, errContinuationAckLost)
	view, err := receipts.GetWorkContinuation(ctx, id)
	require.NoError(t, err)
	require.True(t, view.Pending)
	require.Empty(t, starts, "response loss preceded outbound delivery")
	require.NoError(t, h.Recover(ctx, id))
	require.Equal(t, 1, capacity.calls)
	require.Empty(t, starts)
	view, err = receipts.GetWorkContinuation(ctx, id)
	require.NoError(t, err)
	require.False(t, view.Pending)
	recovery := command.NewRecoverWorkHandler(time.Now, f.sessions, factory, &commandtest.InputBuffer{}, command.NewInputAcceptance(f.sessions, &commandtest.InputBuffer{}), &commandtest.InputSettlements{}, nil, nil)
	require.NoError(t, recovery.Handle(ctx, id))
	require.Len(t, starts, 1)
	require.Equal(t, id, starts[0].WorkID)
	require.Equal(t, domain.ContextContinue, starts[0].ContextMode)
}

func TestContinuationRetainsOriginalRequestBeforeAnyAdmission(t *testing.T) {
	f, id := stoppedContinuationSession(t)
	ctx := context.Background()
	before, err := f.sessions.Get(ctx, id)
	require.NoError(t, err)
	receipts := infrastructure.NewContinuationRequests(f.db, time.Now)
	capacity := &continuationCapacity{}
	factory := executionPort{}
	lost := &lostContinuationReceipt{ContinuationRequests: receipts, retain: true}
	h := command.NewResumeWorkHandler(time.Now, f.sessions, f.agents, access.NewWorkAuthorizer(infrastructure.NewWorkAccessReader(f.db)), computertest.Checked(capacity), factory, lost, querytest.ProvidedModels(), commandtest.UnchangedPlugins{})
	request := command.StartWork{SubjectID: "owner", WorkID: id, RequestID: uuid.NewString()}
	err = h.Handle(ctx, request)
	require.ErrorIs(t, err, errContinuationAckLost)
	require.NoError(t, h.Recover(ctx, id))
	require.Zero(t, capacity.calls, "pending request observation does not submit Resume")
	view, err := receipts.GetWorkContinuation(ctx, id)
	require.NoError(t, err)
	require.True(t, view.Pending)
	client := startupRPCClient(t, f.db)
	readStartup := func() *agentprivatev1.GetWorkStartupResponse {
		t.Helper()
		req := &agentprivatev1.GetWorkStartupRequest{SubjectId: "owner", WorkId: id}
		reqCtx, reqInfo := connect.NewClientContext(ctx)
		reqInfo.RequestHeader().Set("Authorization", "Bearer control")
		result, err := client.GetWorkStartup(reqCtx, req)
		require.NoError(t, err)
		require.Equal(t, request.RequestID, result.ContinuationRequestId)
		return result
	}
	require.True(t, readStartup().ContinuationPending)
	// A lifecycle mutation based on a pre-reservation snapshot must reload;
	// otherwise archive could overlook the newly retained pending request.
	require.NoError(t, before.Close(ctx, uuid.NewString(), domain.ClosureFacts{}, nil))
	require.ErrorIs(t, f.sessions.Save(ctx, before), domain.ErrConcurrentModification)
	retry := request
	retry.RequestID = uuid.NewString()
	require.NoError(t, h.Handle(ctx, retry))
	_, found := admittedStart(t, f.sessions, request.RequestID)
	require.True(t, found, "an explicit retry resumes the original unresolved request")
	_, found = admittedStart(t, f.sessions, retry.RequestID)
	require.False(t, found)
	require.Equal(t, 1, capacity.calls)
	require.False(t, readStartup().ContinuationPending)
}

func TestContinuationConcurrentReservationAndIdentityCollision(t *testing.T) {
	f, id := stoppedContinuationSession(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	before, err := f.sessions.Get(ctx, id)
	require.NoError(t, err)
	other, err := xorm.NewEngine("mysql", f.dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = other.Close() })
	stores := []*infrastructure.ContinuationRequests{infrastructure.NewContinuationRequests(f.db, time.Now), infrastructure.NewContinuationRequests(other, time.Now)}
	type outcome struct {
		request command.ContinuationRequest
		err     error
	}
	results := make(chan outcome, 2)
	start := make(chan struct{})
	var group sync.WaitGroup
	for _, store := range stores {
		group.Go(func() {
			<-start
			request, err := store.RetainContinuation(ctx, command.ContinuationRequest{StartWork: command.StartWork{SubjectID: "owner", WorkID: id, RequestID: uuid.NewString()}, Operation: "resume"}, before.Version)
			results <- outcome{request, err}
		})
	}
	close(start)
	group.Wait()
	close(results)
	requestID := ""
	for result := range results {
		require.NoError(t, result.err)
		if requestID == "" {
			requestID = result.request.RequestID
		}
		require.Equal(t, requestID, result.request.RequestID, "competing callers retain one original request")
	}
	after, err := f.sessions.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, before.Version+1, after.Version)
	require.Equal(t, before.State, after.State)
	ids, err := stores[0].ListPendingContinuations(ctx, "", 100)
	require.NoError(t, err)
	require.Equal(t, []string{id}, ids)
	ids, err = stores[0].ListPendingContinuations(ctx, id, 100)
	require.NoError(t, err)
	require.Empty(t, ids)
	create := command.NewCreateWorkHandler(time.Now, f.agents, f.sessions, f.sessions, access.NewWorkAuthorizer(infrastructure.NewWorkAccessReader(f.db)), agentquery.NewModelConfigurations(agentquery.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "deployment-default"}}), commandtest.StartCatalog(&creationCatalog{}, creationConnections{}), computertest.Checked(startupAuthority{}), executionPort{}, creationPreparation(f.db, executionPort{}, agentquery.NewModelConfigurations(agentquery.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "deployment-default"}})))
	otherID, err := create.Handle(ctx, command.CreateWork{RequestID: uuid.NewString(), SubjectID: "owner", AgentID: before.AgentID, WorkspaceReference: uuid.NewString(), Subject: domain.WorkSubject{Kind: "case", ID: uuid.NewString()}, ProjectID: before.ProjectID})
	require.NoError(t, err)
	otherBefore, err := f.sessions.Get(ctx, otherID)
	require.NoError(t, err)
	_, err = stores[1].RetainContinuation(ctx, command.ContinuationRequest{StartWork: command.StartWork{SubjectID: "owner", WorkID: otherID, RequestID: requestID}, Operation: "resume"}, otherBefore.Version)
	require.ErrorIs(t, err, command.ErrContinuationConflict)
	otherAfter, err := f.sessions.Get(ctx, otherID)
	require.NoError(t, err)
	require.Equal(t, otherBefore.Version, otherAfter.Version, "identity collision rolls back the Session token update")
	_, found, err := stores[1].GetContinuation(ctx, otherID)
	require.NoError(t, err)
	require.False(t, found)
}

// A withdrawn Model Configuration refuses Resume with that reason and leaves
// the Work as it was; once the platform provides it again, the Work resumes.
func TestContinuationRefusesAWithdrawnModelConfigurationUntilItIsProvidedAgain(t *testing.T) {
	f, id := stoppedContinuationSession(t)
	ctx := context.Background()
	before, err := f.sessions.Get(ctx, id)
	require.NoError(t, err)
	starts := []domain.StartExecutionRequest{}
	factory := startupExecutionPort{onStart: func(r domain.StartExecutionRequest) error { starts = append(starts, r); return nil }}
	receipts := infrastructure.NewContinuationRequests(f.db, time.Now)
	withdrawn := command.NewResumeWorkHandler(time.Now, f.sessions, f.agents, access.NewWorkAuthorizer(infrastructure.NewWorkAccessReader(f.db)), computertest.Checked(&continuationCapacity{}), factory, receipts, agentquery.NewModelConfigurations(), commandtest.UnchangedPlugins{})
	request := command.StartWork{SubjectID: "owner", WorkID: id, RequestID: uuid.NewString()}
	require.ErrorIs(t, withdrawn.Handle(ctx, request), domain.ErrModelConfigurationWithdrawn)
	require.ErrorIs(t, withdrawn.Handle(ctx, request), domain.ErrModelConfigurationWithdrawn, "the retained refusal keeps its reason")
	require.Empty(t, starts)
	retained, err := f.sessions.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, domain.WorkStopped, retained.State)
	require.Equal(t, before.HarnessProcess, retained.HarnessProcess)

	provided := command.NewResumeWorkHandler(time.Now, f.sessions, f.agents, access.NewWorkAuthorizer(infrastructure.NewWorkAccessReader(f.db)), computertest.Checked(&continuationCapacity{}), factory, receipts, querytest.ProvidedModels(), commandtest.UnchangedPlugins{})
	require.NoError(t, provided.Handle(ctx, command.StartWork{SubjectID: "owner", WorkID: id, RequestID: uuid.NewString()}))
	require.Len(t, starts, 1)
	require.Equal(t, before.WorkModel, starts[0].WorkModel)
}

// unreachableComputer refuses every run as its Managed Computer being away.
type unreachableComputer struct{ command.ComputerPort }

func (unreachableComputer) AdmitRun(context.Context, command.RunAdmission) (command.ComputerAnswer, error) {
	return command.ComputerAnswer{Kind: command.ComputerRejected, Rejection: domain.ErrComputerUnreachable}, nil
}

// A Resume refused because the Computer is unreachable starts and fails
// nothing: the Work's view keeps its earlier start's startup facts and
// says the Resume is requested on reconnection (Agent model: Capacity
// rejection state, Retry on reconnection).
func TestContinuationRefusedAsUnreachableLeavesNoFailedStartup(t *testing.T) {
	f, id := stoppedContinuationSession(t)
	ctx := context.Background()
	factory := executionPort{}
	receipts := infrastructure.NewContinuationRequests(f.db, time.Now)
	handler := command.NewResumeWorkHandler(time.Now, f.sessions, f.agents, access.NewWorkAuthorizer(infrastructure.NewWorkAccessReader(f.db)), unreachableComputer{computertest.Checked(&continuationCapacity{})}, factory, receipts, querytest.ProvidedModels(), commandtest.UnchangedPlugins{})
	views := infrastructure.NewWorkQueryRepository(f.db)
	before, err := views.GetWorkView(ctx, id)
	require.NoError(t, err)
	require.False(t, before.ResumeOnReconnection)

	err = handler.Handle(ctx, command.StartWork{SubjectID: "owner", WorkID: id, RequestID: uuid.NewString()})
	require.ErrorIs(t, err, domain.ErrComputerUnreachable)
	view, err := views.GetWorkView(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "stopped", view.State)
	require.True(t, view.ResumeOnReconnection, "the refused Resume is owed on reconnection")
	require.NotNil(t, view.StartupProgress)
	require.NotEqual(t, "failed", view.StartupProgress.Status, "the refusal shows no failed startup")
	require.Equal(t, before.StartupProgress.RequestID, view.StartupProgress.RequestID)
	require.Equal(t, before.StartupProgress.Status, view.StartupProgress.Status)
	require.Equal(t, before.StartupProgress.Reason, view.StartupProgress.Reason)
}
