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
	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/application/query/querytest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"github.com/talgent-work/workbench/internal/testsupport/computertest"
	"xorm.io/xorm"
)

// The HTTP/application/MySQL query derives a Work's start requests from its
// facts: its creation request, a held initial task input, its latest Restart
// and its closure.
func TestSessionStartupRPCDerivesCreationRestartAndClosure(t *testing.T) {
	f := newWorkCreationFixture(t)
	ctx := context.Background()
	create := command.NewCreateWorkHandler(time.Now, f.agents, f.sessions, access.NewWorkAuthorizer(infrastructure.NewWorkAccessReader(f.db)), query.NewModelConfigurations(query.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "deployment-default"}}), commandtest.StartCatalog(&creationCatalog{}, creationConnections{}), computertest.Checked(startupAuthority{}), creationPreparation(f.db, deploymentModels()))
	sessionID, err := create.Handle(ctx, f.request)
	require.NoError(t, err)
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
	require.Equal(t, f.request.RequestID, initial.GetInitialRequestId())
	require.True(t, initial.GetCreationPending())
	require.Empty(t, initial.GetContinuationRequestId())
	require.False(t, initial.GetRejected(), "nothing records a rejected first start")
	require.False(t, initial.GetCleanupConfirmed())
	require.False(t, initial.GetContinuationPending())

	require.NoError(t, command.NewInputAcceptance(f.sessions, &commandtest.InputBuffer{}).HandOffInitialTaskInput(ctx, sessionID))
	require.False(t, read().GetCreationPending(), "the handed-off initial task input leaves the Work")
	session, err := f.sessions.Get(ctx, sessionID)
	require.NoError(t, err)
	reportExit(t, session, domain.HarnessExit{ExitCode: 1, Reason: "startup failed"})
	reportReleased(t, session)
	require.NoError(t, f.sessions.Save(ctx, session))
	originalCapabilities := session.CapabilitySnapshot.Clone()
	originalContext := session.HarnessSessionReference

	// Rebuild the read application on another physical connection: the facts
	// come from the Work's own rows, not an in-memory cache.
	other, err := xorm.NewEngine("mysql", f.dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = other.Close() })
	client = startupRPCClient(t, other)
	withOther := read()
	require.False(t, withOther.GetCreationPending())
	require.Equal(t, f.request.RequestID, withOther.GetInitialRequestId())
	require.False(t, withOther.GetCleanupConfirmed())

	// Restart keeps the creation request and names its own request.
	restartID := uuid.NewString()
	restart := command.NewRestartWorkHandler(time.Now, f.sessions, f.agents, access.NewWorkAuthorizer(infrastructure.NewWorkAccessReader(f.db)), computertest.Checked(startupAuthority{}), querytest.ProvidedModels(), commandtest.UnchangedPlugins{})
	_, err = restart.Handle(ctx, command.StartWork{SubjectID: "owner", WorkID: sessionID, RequestID: restartID})
	require.NoError(t, err)
	afterRestart := read()
	require.Equal(t, f.request.RequestID, afterRestart.GetInitialRequestId())
	require.Equal(t, restartID, afterRestart.GetContinuationRequestId())
	require.False(t, afterRestart.GetContinuationPending(), "an admitted Restart is committed with its start")
	require.False(t, afterRestart.GetCleanupConfirmed())
	require.False(t, afterRestart.GetCreationPending())
	session, err = f.sessions.Get(ctx, sessionID)
	require.NoError(t, err)
	require.Equal(t, domain.WorkStarting, session.State)
	require.Equal(t, originalCapabilities, session.CapabilitySnapshot)
	require.Equal(t, originalContext, session.HarnessSessionReference)

	foreign := f.request
	foreignInput := *foreign.InitialExecution
	foreignInput.RequestID, foreignInput.Input.ID = uuid.NewString(), uuid.NewString()
	foreign.InitialExecution, foreign.RequestID = &foreignInput, foreignInput.RequestID
	foreign.Subject = domain.WorkSubject{Kind: "case", ID: uuid.NewString()}
	_, err = create.Handle(ctx, foreign)
	require.NoError(t, err)
	require.False(t, read().GetCreationPending(), "another Subject cannot block this Session")

	reportExit(t, session, domain.HarnessExit{ExitCode: 1, Reason: "startup failed again"})
	reportReleased(t, session)
	require.NoError(t, f.sessions.Save(ctx, session))
	require.False(t, read().GetCleanupConfirmed(), "a failed Work's preparation is not ended before it closes")
	closeWork := command.NewCloseWorkHandler(f.sessions, access.NewWorkAuthorizer(infrastructure.NewWorkAccessReader(f.db)), executionsOf(f.db), &commandtest.InputBuffer{})
	require.NoError(t, closeWork.Handle(ctx, command.WorkControl{SubjectID: "owner", WorkID: sessionID, RequestID: uuid.NewString()}))
	require.True(t, read().GetCleanupConfirmed(), "a closed Work receives no further repository access")

	withoutInputRequest := uuid.NewString()
	withoutInput, err := create.Handle(ctx, command.CreateWork{RequestID: withoutInputRequest, AgentID: f.request.AgentID, SubjectID: "owner", WorkspaceReference: uuid.NewString(), Subject: domain.WorkSubject{Kind: "case", ID: uuid.NewString()}, ProjectID: f.request.ProjectID})
	require.NoError(t, err)
	reader := infrastructure.NewExecutionQueryRepository(other)
	absent, err := reader.GetWorkStartup(ctx, withoutInput)
	require.NoError(t, err)
	require.Equal(t, withoutInput, absent.WorkID)
	require.Equal(t, withoutInputRequest, absent.InitialRequestID, "every created Work has its creation request")
	require.Empty(t, absent.ContinuationRequestID)
	require.False(t, absent.CleanupConfirmed)
	require.False(t, absent.CreationPending, "a Work created without an initial task input holds none")
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

}
