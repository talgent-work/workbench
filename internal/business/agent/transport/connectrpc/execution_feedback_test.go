package connectrpc_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"
	"github.com/talgent-work/workbench/internal/testsupport/connecttest"

	"github.com/google/uuid"
	dispatchercommand "github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/agentfeedback"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/stretchr/testify/require"
	agentprivatev1 "github.com/talgent-work/contracts/gen/go/agent/private/v1"
	"github.com/talgent-work/contracts/gen/go/agent/private/v1/agentprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	transport "github.com/talgent-work/workbench/internal/business/agent/transport/connectrpc"
)

func TestFeedbackRPCAuthenticatesBeforeAdvancingWork(t *testing.T) {
	ctx := context.Background()
	agent, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"owner"}, AgentName: "Fixture Agent", Purpose: "work", UsageScope: domain.UsageScope{Kind: "employee", Reference: "employee"}, HomeBinding: domain.HomeBinding{OwnerID: "user-home", Namespace: "project"}, Capabilities: domain.CapabilityConfiguration{Prompt: "review"}})
	require.NoError(t, err)
	port := executionPort{}
	session, err := domain.NewAgentWork(context.Background(), domain.WorkInput{Subject: domain.WorkSubject{Kind: "case", ID: "case"}, ProjectID: "project", WorkModel: domain.WorkModelSelection{ConfigurationID: "default"}, StartedBy: "owner", ID: uuid.Must(uuid.NewV7()).String(), HomeBinding: domain.HomeBinding{OwnerID: "user-home", Namespace: "project"}, AgentID: agent.ID, Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}, Prompt: "review"}, WorkspaceReference: "workspace"}, domain.StartAdmission{AdmittedAt: time.Now(), AgentID: agent.ID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", DeskID: "desk-test"}, ModelConfigurationProvided: true}, port)
	require.NoError(t, err)
	store := &sessionStore{session: session}
	app := &application.Application{Commands: application.Commands{ApplyExecutionFeedback: command.NewApplyExecutionFeedbackHandler(time.Now, store, &rpcProcesses{}, infrastructure.NewExecutionRequestsFactory(port), &commandtest.InputBuffer{}, &commandtest.InputSettlements{})}}
	handler := connecttest.Handler(func(s *connect.Server) {
		agentprivatev1connect.RegisterExecutionFeedbackServiceHandler(s, transport.NewFeedbackHandler(app, nil, "integration-secret"))
	})
	server := httptest.NewServer(handler)
	defer server.Close()
	client := agentprivatev1connect.NewExecutionFeedbackServiceClient(connect.NewClient(connecthttp.NewTransport(server.Client(), server.URL, connecthttp.WithReadMaxBytes(0))))
	report := func(token string, kind agentprivatev1.FeedbackKind) error {
		req := &agentprivatev1.ReportRequest{WorkId: session.ID, Kind: kind, ProcessId: "process"}
		ctx, info := connect.NewClientContext(ctx)
		if token != "" {
			info.RequestHeader().Set("Authorization", "Bearer "+token)
		}
		_, err := client.Report(ctx, req)
		return err
	}
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(report("", agentprivatev1.FeedbackKind_FEEDBACK_KIND_PREPARED)))
	require.Zero(t, store.reads)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(report("wrong", agentprivatev1.FeedbackKind_FEEDBACK_KIND_PREPARED)))
	require.Zero(t, store.reads)
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(report("integration-secret", agentprivatev1.FeedbackKind(99))))
	require.Zero(t, store.reads)
	require.NoError(t, report("integration-secret", agentprivatev1.FeedbackKind_FEEDBACK_KIND_PREPARED))
	require.Equal(t, domain.WorkStarting, store.session.State)
	reporter := agentfeedback.NewClient(server.Client(), server.URL, "integration-secret")
	require.NoError(t, reporter.ReportExecution(ctx, dispatchercommand.ExecutionObservation{WorkID: session.ID, ProcessID: "process", Kind: dispatchercommand.ExecutionStarted}))
	require.Equal(t, domain.WorkWorking, store.session.State)
	require.Equal(t, 2, store.saves)
	plan := dispatchercommand.ExecutionObservation{WorkID: session.ID, ProcessID: "process", Kind: dispatchercommand.ExecutionWorkProgressUpdated, ObservationSequence: 10,
		WorkProgress: []dispatchercommand.WorkProgressItem{{Content: "Verify Work progress", Status: "in_progress", ActiveForm: "Verifying Work progress"}}}
	require.NoError(t, reporter.ReportExecution(ctx, plan))
	require.Equal(t, []domain.WorkProgressItem{{Content: "Verify Work progress", Status: "in_progress", ActiveForm: "Verifying Work progress"}}, store.session.Progress.Items)
	plan.ObservationSequence = 20
	plan.WorkProgress = nil
	require.NoError(t, reporter.ReportExecution(ctx, plan))
	require.NotNil(t, store.session.Progress)
	require.Empty(t, store.session.Progress.Items)
	require.NoError(t, reporter.ReportExecution(ctx, dispatchercommand.ExecutionObservation{WorkID: session.ID, ProcessID: "process", Kind: dispatchercommand.ExecutionTurnStarted, ObservationSequence: 25}))
	observation := dispatchercommand.ExecutionObservation{WorkID: session.ID, ProcessID: "process", Kind: dispatchercommand.ExecutionUserInteractionRequired, InteractionID: "wait-1", ObservationSequence: 30, RequiredInteractionIDs: []string{"wait-1"}}
	require.NoError(t, reporter.ReportExecution(ctx, observation))
	require.True(t, store.session.Waiting())
	observation.Kind = dispatchercommand.ExecutionUserInteractionResolved
	observation.RequiredInteractionIDs = nil
	observation.ObservationSequence = 40
	require.NoError(t, reporter.ReportExecution(ctx, observation))
	require.False(t, store.session.Waiting(), "the running Turn's question is resolved")
	require.NoError(t, reporter.ReportExecution(ctx, dispatchercommand.ExecutionObservation{WorkID: session.ID, ProcessID: "process", Kind: dispatchercommand.ExecutionTurnFinished, ObservationSequence: 45, TurnCompleted: true}))
	require.True(t, store.session.Idle(), "nothing is left to process")
	require.NoError(t, reporter.ReportExecution(ctx, dispatchercommand.ExecutionObservation{WorkID: session.ID, ProcessID: "process", Kind: dispatchercommand.ExecutionTurnActivity, ObservationSequence: 46}))
	require.False(t, store.session.Idle(), "Turn activity ends idleness")
	require.Equal(t, 9, store.saves)
	for _, fact := range []dispatchercommand.ExecutionObservation{
		{Kind: dispatchercommand.ExecutionTurnStarted, ObservationSequence: 100},
		{Kind: dispatchercommand.ExecutionTurnFinished, ObservationSequence: 200, TurnCompleted: true},
		{Kind: dispatchercommand.ExecutionTurnStarted, ObservationSequence: 300},
		{Kind: dispatchercommand.ExecutionTurnStarted, ObservationSequence: 300},
	} {
		fact.WorkID, fact.ProcessID = session.ID, "process"
		require.NoError(t, reporter.ReportExecution(ctx, fact))
	}
	require.Equal(t, domain.TurnObservation{Sequence: 300, Active: true}, store.session.HarnessProcess.Turn)
	require.Equal(t, domain.WorkWorking, store.session.State)
}

type sessionStore struct {
	session      *domain.AgentWork
	reads, saves int
}

func (s *sessionStore) Get(context.Context, string) (*domain.AgentWork, error) {
	s.reads++
	clone := *s.session
	observation := *clone.HarnessProcess
	clone.HarnessProcess = &observation
	return &clone, nil
}
func (s *sessionStore) Save(_ context.Context, session *domain.AgentWork) error {
	s.saves++
	s.session = session
	return nil
}

type executionPort struct{}

func (executionPort) StartExecution(context.Context, domain.StartExecutionRequest) error { return nil }
func (executionPort) StopExecution(context.Context, string, string) error {
	return nil
}
func (executionPort) ReclaimCompute(context.Context, string) error { return nil }

func (executionPort) RetireFencedRun(context.Context, string) error { return nil }

func (executionPort) CancelRunningTurn(context.Context, string) error { return nil }
