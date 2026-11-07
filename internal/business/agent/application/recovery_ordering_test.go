package application_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"
)

// recoveredWorks holds one Work; each read is a copy, so only a save changes it.
type recoveredWorks struct{ work domain.AgentWork }

func (s *recoveredWorks) Get(context.Context, string) (*domain.AgentWork, error) {
	return s.copy(s.work), nil
}

func (s *recoveredWorks) Save(_ context.Context, work *domain.AgentWork) error {
	s.work = *s.copy(*work)
	return nil
}

func (*recoveredWorks) copy(work domain.AgentWork) *domain.AgentWork {
	if work.HarnessProcess != nil {
		process := *work.HarnessProcess
		work.HarnessProcess = &process
	}
	return &work
}

// unconnectedExecution is the execution of a start that never connected a
// Harness process: Dispatcher cannot deliver a stop to it.
type unconnectedExecution struct {
	stopErr  error
	reclaims int
}

func (*unconnectedExecution) StartExecution(context.Context, domain.StartExecutionRequest) error {
	return nil
}
func (e *unconnectedExecution) StopExecution(context.Context, string, string) error {
	return e.stopErr
}
func (*unconnectedExecution) CancelRunningTurn(context.Context, string) error { return nil }
func (e *unconnectedExecution) ReclaimCompute(context.Context, string) error {
	e.reclaims++
	return nil
}
func (*unconnectedExecution) RetireFencedRun(context.Context, string) error { return nil }

// observedProcesses reports one observation of the Work's process.
type observedProcesses struct {
	observation domain.ProcessObservation
	err         error
}

func (p observedProcesses) ObserveProcess(context.Context, string) (command.WorkProcessObservation, error) {
	return command.WorkProcessObservation{ProcessObservation: p.observation}, p.err
}
func (observedProcesses) IsCurrentProcess(context.Context, string, string) (bool, error) {
	return false, nil
}
func (observedProcesses) AcknowledgeExit(context.Context, string, string) (bool, error) {
	return false, nil
}

// failedStart is a Work whose start failed at its Startup Deadline before any
// Harness process connected; it retains the request to end that start.
func failedStart(t *testing.T) *recoveredWorks {
	t.Helper()
	ctx := context.Background()
	agent := uuid.NewString()
	seated := time.Now().Add(-domain.StartupDeadline - 2*time.Minute)
	work, err := domain.NewAgentWork(ctx, domain.WorkInput{Subject: domain.WorkSubject{Kind: "case", ID: "case"}, ProjectID: "project", WorkModel: domain.WorkModelSelection{ConfigurationID: "default"}, StartedBy: "owner", Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}}, ID: uuid.Must(uuid.NewV7()).String(), HomeBinding: domain.HomeBinding{OwnerID: "owner", Namespace: "project"}, AgentID: agent, WorkspaceReference: "workspace"},
		domain.StartAdmission{AdmittedAt: seated, AgentID: agent, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", DeskID: "desk"}, ModelConfigurationProvided: true}, &unconnectedExecution{})
	require.NoError(t, err)
	failed, err := work.FailAtStartupDeadline(ctx, time.Now(), &unconnectedExecution{})
	require.NoError(t, err)
	require.True(t, failed)
	return &recoveredWorks{work: *work}
}

func recoveryOf(works *recoveredWorks, execution domain.ExecutionPort, processes command.ProcessObservationPort) *application.Recovery {
	requests := infrastructure.NewExecutionRequestsFactory(execution)
	buffer, settlements := &commandtest.InputBuffer{}, &commandtest.InputSettlements{}
	app := &application.Application{Commands: application.Commands{
		RecoverWork:            command.NewRecoverWorkHandler(time.Now, works, requests, buffer, settlements, nil, nil),
		ApplyExecutionFeedback: command.NewApplyExecutionFeedbackHandler(time.Now, works, processes, requests, buffer, settlements),
		CloseWork:              command.NewCloseWorkHandler(works, nil, requests, commandtest.NoClosureObligations{}, commandtest.NoClosureObligations{}, commandtest.NoClosureObligations{}, buffer),
	}}
	return application.NewRecovery(app, nil, nil, nil, nil)
}

// A retained stop Dispatcher cannot deliver does not hold back the Work's
// observation: the start reported abandoned reaches its compute release, and
// nothing is left to retry.
func TestRecoveryAppliesTheObservationBeforeTheRetainedRequests(t *testing.T) {
	works := failedStart(t)
	execution := &unconnectedExecution{stopErr: errors.New("dispatcher: no connection to stop")}
	recovery := recoveryOf(works, execution, observedProcesses{observation: domain.ProcessObservation{State: domain.ProcessStartAbandoned}})

	require.NoError(t, recovery.RecoverWork(context.Background(), works.work.ID))
	require.True(t, works.work.HarnessProcess.StartAbandoned, "the abandonment is saved")
	require.Positive(t, execution.reclaims, "the abandoned start's compute release is requested")
	require.Equal(t, domain.WorkFailed, works.work.State)
}

// Each recovery step's failure is reported, and none holds back another.
func TestRecoveryReturnsEveryStepFailureTogether(t *testing.T) {
	works := failedStart(t)
	stopErr, observeErr := errors.New("dispatcher: no connection to stop"), errors.New("dispatcher unavailable")
	recovery := recoveryOf(works, &unconnectedExecution{stopErr: stopErr}, observedProcesses{err: observeErr})

	err := recovery.RecoverWork(context.Background(), works.work.ID)
	require.ErrorIs(t, err, observeErr)
	require.ErrorIs(t, err, stopErr)
	require.ErrorIs(t, err, domain.ErrExecutionRequest)
}
