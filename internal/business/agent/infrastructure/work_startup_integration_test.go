//go:build integration

package infrastructure_test

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	agentprivatev1 "github.com/talgent-work/contracts/gen/go/agent/private/v1"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/application/query/querytest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"github.com/talgent-work/workbench/internal/testsupport/computertest"
	"xorm.io/xorm"
)

// The HTTP/application/MySQL query distinguishes admitted startup failure,
// unrelated pending creation, explicit Restart and acknowledged cleanup.
func TestSessionStartupRPCDistinguishesFailureRestartAndCleanup(t *testing.T) {
	f := newWorkCreationFixture(t)
	ctx := context.Background()
	create := command.NewCreateWorkHandler(time.Now, f.agents, f.sessions, f.sessions, infrastructure.NewWorkAuthorizer(f.db), query.NewModelConfigurations(query.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "deployment-default"}}), &creationCatalog{}, creationConnections{}, f.sessions, computertest.Checked(startupAuthority{}), infrastructure.NewExecutionRequestsFactory(executionPort{}))
	sessionID, err := create.Handle(ctx, f.request.Creation)
	require.NoError(t, err)
	store := infrastructure.NewInitialExecutionRequests(f.db, time.Now)
	client := startupRPCClient(t, f.db)
	read := func() *agentprivatev1.GetWorkStartupResponse {
		t.Helper()
		req := &agentprivatev1.GetWorkStartupRequest{SubjectId: "owner", WorkId: sessionID}
		reqCtx, reqInfo := connect.NewClientContext(ctx)
		reqInfo.RequestHeader().Set("Authorization", "Bearer control")
		result, err := client.GetWorkStartup(reqCtx, req)
		require.NoError(t, err)
		require.Equal(t, sessionID, result.GetWorkId())
		return result
	}
	initial := read()
	require.Equal(t, f.request.ID, initial.GetInitialRequestId())
	require.True(t, initial.GetCreationPending())
	require.False(t, initial.GetRejected())
	require.False(t, initial.GetCleanupConfirmed())

	require.NoError(t, f.requests.ConfirmWorkCreation(ctx, f.request.ID, sessionID))
	require.True(t, read().GetCreationPending(), "association confirmation alone does not finish initial-input handoff")
	require.NoError(t, f.requests.CompleteWorkCreation(ctx, f.request.ID))
	require.False(t, read().GetCreationPending())
	require.False(t, read().GetRejected(), "an unresolved initial startup must not appear rejected")
	retained, err := store.Get(ctx, sessionID)
	require.NoError(t, err)
	retained.Rejected, retained.StartFailed = true, true
	require.NoError(t, store.Save(ctx, retained))
	require.True(t, read().GetRejected())
	require.False(t, read().GetCleanupConfirmed(), "rejection cannot stand in for completed resource release")
	session, err := f.sessions.Get(ctx, sessionID)
	require.NoError(t, err)
	require.NoError(t, session.RecordExit(ctx, domain.HarnessExit{ExitCode: 1, Reason: "startup failed"}, executionPort{}))
	require.NoError(t, session.ConfirmComputeReclaimed())
	require.NoError(t, f.sessions.Save(ctx, session))
	originalCapabilities := session.CapabilitySnapshot.Clone()
	originalContext := session.HarnessSessionReference

	// A separate creation request may be retained before association is known.
	// Its empty Work ID does not remove the pending obligation for this Subject.
	retryCreation := f.request.Creation
	retryInput := *retryCreation.InitialExecution
	retryInput.RequestID, retryInput.Input.ID = uuid.NewString(), uuid.NewString()
	retryCreation.InitialExecution = &retryInput
	retryCreation.RequestID = retryInput.RequestID
	retry, err := command.NewWorkCreationRequest(retryCreation)
	require.NoError(t, err)
	retry, err = f.requests.RetainWorkCreation(ctx, retry)
	require.NoError(t, err)
	require.Empty(t, retry.WorkID)
	withRetry := read()
	require.True(t, withRetry.GetCreationPending(), "an unbound new request for the same binding is still pending")
	require.Equal(t, f.request.ID, withRetry.GetInitialRequestId(), "the query must not invent a handoff")
	require.True(t, withRetry.GetRejected())
	require.False(t, withRetry.GetCleanupConfirmed())

	// Rebuild the read application on another physical connection. Pending
	// startup must remain visible without an in-memory cache or Work row.
	other, err := xorm.NewEngine("mysql", f.dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = other.Close() })
	client = startupRPCClient(t, other)
	require.True(t, read().GetCreationPending())
	_, err = f.requests.CancelWorkCreation(ctx, retry.ID)
	require.NoError(t, err)
	require.False(t, read().GetCreationPending(), "a cancelled request owes nothing more")

	// Restart retains the original input identity and resets the failed
	// preparation; it does not replace the initial receipt with a new task.
	restart := command.NewRestartWorkHandler(time.Now, f.sessions, f.agents, infrastructure.NewWorkAuthorizer(f.db), computertest.Checked(startupAuthority{}), infrastructure.NewExecutionRequestsFactory(executionPort{}), f.sessions, querytest.ProvidedModels(), commandtest.UnchangedPlugins{})
	_, err = restart.Handle(ctx, command.StartWork{SubjectID: "owner", WorkID: sessionID, RequestID: uuid.NewString()})
	require.NoError(t, err)
	afterRestart := read()
	require.Equal(t, f.request.ID, afterRestart.GetInitialRequestId())
	require.False(t, afterRestart.GetRejected())
	require.False(t, afterRestart.GetCleanupConfirmed())
	require.False(t, afterRestart.GetCreationPending())
	retained, err = store.Get(ctx, sessionID)
	require.NoError(t, err)
	require.Equal(t, f.request.Creation.InitialExecution.Input, retained.Input)
	require.False(t, retained.InputBuffered)
	session, err = f.sessions.Get(ctx, sessionID)
	require.NoError(t, err)
	require.Equal(t, domain.WorkStarting, session.State)
	require.Equal(t, originalCapabilities, session.CapabilitySnapshot)
	require.Equal(t, originalContext, session.HarnessSessionReference)

	foreign := retryCreation
	foreignInput := *foreign.InitialExecution
	foreignInput.RequestID = uuid.NewString()
	foreign.InitialExecution = &foreignInput
	foreign.RequestID = foreignInput.RequestID
	foreign.Subject, foreign.ProjectID = domain.WorkSubject{Kind: "case", ID: uuid.NewString()}, f.request.Creation.ProjectID
	unrelated, err := command.NewWorkCreationRequest(foreign)
	require.NoError(t, err)
	_, err = f.requests.RetainWorkCreation(ctx, unrelated)
	require.NoError(t, err)
	require.False(t, read().GetCreationPending(), "another Subject cannot block this Session")

	require.NoError(t, session.RecordExit(ctx, domain.HarnessExit{ExitCode: 1, Reason: "startup failed again"}, executionPort{}))
	require.NoError(t, session.ConfirmComputeReclaimed())
	require.NoError(t, f.sessions.Save(ctx, session))
	closeWork := command.NewCloseWorkHandler(f.sessions, infrastructure.NewWorkAuthorizer(f.db), infrastructure.NewExecutionRequestsFactory(executionPort{}), commandtest.NoClosureObligations{}, commandtest.NoClosureObligations{}, commandtest.NoClosureObligations{}, &commandtest.InputBuffer{})
	require.NoError(t, closeWork.Handle(ctx, command.WorkControl{SubjectID: "owner", WorkID: sessionID, RequestID: uuid.NewString()}))
	retained, err = store.Get(ctx, sessionID)
	require.NoError(t, err)
	retained.Rejected, retained.StartFailed, retained.CleanupConfirmed = true, true, true
	require.NoError(t, store.Save(ctx, retained))
	require.True(t, read().GetRejected())
	require.True(t, read().GetCleanupConfirmed(), "cleanup is reported only after its explicit acknowledgement")

	withoutInput, err := create.Handle(ctx, command.CreateWork{RequestID: uuid.NewString(), AgentID: f.request.Creation.AgentID, SubjectID: "owner", WorkspaceReference: uuid.NewString(), Subject: domain.WorkSubject{Kind: "case", ID: uuid.NewString()}, ProjectID: f.request.Creation.ProjectID})
	require.NoError(t, err)
	reader := infrastructure.NewExecutionQueryRepository(other)
	absent, err := reader.GetWorkStartup(ctx, withoutInput)
	require.NoError(t, err)
	require.Equal(t, withoutInput, absent.WorkID)
	require.Empty(t, absent.InitialRequestID)
	require.False(t, absent.Rejected)
	require.False(t, absent.CleanupConfirmed, "missing preparation cannot be reported as completed cleanup")
	require.False(t, absent.CreationPending)
	_, err = reader.GetWorkStartup(ctx, uuid.NewString())
	require.ErrorIs(t, err, domain.ErrWorkNotFound)

	for _, tc := range []struct {
		subject, token string
		code           connect.Code
	}{
		{"owner", "invalid", connect.CodeUnauthenticated},
		{"another-owner", "control", connect.CodePermissionDenied},
	} {
		req := &agentprivatev1.GetWorkStartupRequest{SubjectId: tc.subject, WorkId: sessionID}
		reqCtx, reqInfo := connect.NewClientContext(ctx)
		reqInfo.RequestHeader().Set("Authorization", "Bearer "+tc.token)
		_, err := client.GetWorkStartup(reqCtx, req)
		require.Equal(t, tc.code, connect.CodeOf(err))
	}
	session, err = f.sessions.Get(ctx, sessionID)
	require.NoError(t, err)
	require.Equal(t, domain.WorkClosed, session.State)
	require.Equal(t, originalContext, session.HarnessSessionReference)

	// The relation to the Work's Subject is a query key, with a covering pending index.
	plan, err := other.QueryString("EXPLAIN SELECT c.id FROM agent_session_creation_request c WHERE c.subject_digest=? AND c.pending=1 AND c.deleted_at=0", "digest")
	require.NoError(t, err)
	require.Len(t, plan, 1)
	require.Contains(t, plan[0]["possible_keys"], "idx_session_creation_subject_pending")
	require.NotEmpty(t, plan[0]["key"], "the pending lookup should use an available index")
	t.Logf("pending lookup plan: key=%s, rows=%s, extra=%s", plan[0]["key"], plan[0]["rows"], plan[0]["Extra"])
}
