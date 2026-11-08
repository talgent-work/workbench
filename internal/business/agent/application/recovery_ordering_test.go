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
	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"
)

// recoveredWorks holds one Work; each read is a copy, so only a save changes it.
type recoveredWorks struct {
	work    domain.AgentWork
	saveErr error
}

func (s *recoveredWorks) Get(context.Context, string) (*domain.AgentWork, error) {
	if s.work.ID == "" {
		return nil, domain.ErrWorkNotFound
	}
	return s.copy(s.work), nil
}

func (s *recoveredWorks) FindBySubject(context.Context, string, domain.WorkSubject) (*domain.AgentWork, error) {
	return nil, nil
}

func (s *recoveredWorks) Save(_ context.Context, work *domain.AgentWork) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	s.work = *s.copy(*work)
	return nil
}

func (*recoveredWorks) copy(work domain.AgentWork) *domain.AgentWork {
	if work.HarnessProcess != nil {
		process := *work.HarnessProcess
		work.HarnessProcess = &process
	}
	work.Events = domain.NewEventCollection()
	return &work
}

// recoveryWorld is a Work, its executions and the Recovery over them.
type recoveryWorld struct {
	works      *recoveredWorks
	executions *commandtest.Executions
	harness    *commandtest.Harness
	recovery   *application.Recovery
}

// abandonedStart is a Work whose start was seated past its Startup Deadline
// and never launched a Harness process: its execution reported the failed and
// abandoned start, and the Work applied only the failure, which asks the
// start to end.
func abandonedStart(t *testing.T) *recoveryWorld {
	t.Helper()
	agent := uuid.NewString()
	seated := time.Now().Add(-domain.StartupDeadline - 2*time.Minute)
	work, err := domain.NewAgentWork(domain.WorkInput{Subject: domain.WorkSubject{Kind: "case", ID: "case"}, ProjectID: "project", WorkModel: domain.WorkModelSelection{ConfigurationID: "default"}, StartedBy: "owner", Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}}, ID: uuid.Must(uuid.NewV7()).String(), RepresentedPerson: "owner", WorkLayout: domain.DefaultWorkLayout(), AgentID: agent},
		domain.StartAdmission{AdmittedAt: seated, AgentID: agent, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", Kind: domain.ComputerCloud, DeskID: "desk"}, ModelConfigurationProvided: true})
	require.NoError(t, err)
	var admitted domain.WorkStartAdmitted
	for _, request := range work.ExecutionRequests() {
		if start, ok := request.(domain.WorkStartAdmitted); ok {
			admitted = start
		}
	}
	execution, err := domain.BeginExecution(domain.ExecutionBeginning{Admitted: admitted, Launch: work.LaunchSpecification(), Realization: domain.RealizationCloud})
	require.NoError(t, err)
	require.True(t, commandtest.Submit(execution))
	_, err = execution.Reconcile(domain.EnvironmentObservation{}, domain.LeaseTerms{}, time.Now())
	require.NoError(t, err)
	for _, report := range execution.Reports() {
		if _, failed := report.(domain.ExecutionStartFailed); failed {
			_, err = work.ApplyExecutionReport(report)
			require.NoError(t, err)
		}
	}
	require.Equal(t, domain.WorkFailed, work.State)
	executions := &commandtest.Executions{}
	executions.Put(execution)

	w := &recoveryWorld{works: &recoveredWorks{work: *work}, executions: executions, harness: commandtest.NewHarness()}
	settlements := &commandtest.InputSettlements{}
	coordination := command.NewExecutionCoordination(time.Now, w.works, executions, &commandtest.Computers{}, w.harness)
	app := &application.Application{Commands: application.Commands{
		ExecutionCoordination:  coordination,
		RecoverWork:            command.NewRecoverWorkHandler(time.Now, w.works, w.harness, command.NewInputAcceptance(w.works, w.harness), settlements, nil),
		ApplyExecutionFeedback: command.NewApplyExecutionFeedbackHandler(time.Now, w.works, executions, coordination, w.harness, settlements),
		CloseWork:              command.NewCloseWorkHandler(w.works, nil, executions, w.harness.InputBuffer),
	}}
	w.recovery = application.NewRecovery(app, nil, executions, nil, nil, nil, nil)
	return w
}

func (w *recoveryWorld) execution(t *testing.T) *domain.AgentExecution {
	t.Helper()
	execution, err := w.executions.Latest(context.Background(), w.works.work.ID)
	require.NoError(t, err)
	return execution
}

// A retained exit request that cannot reach the execution now does not hold
// back the execution's report: the abandoned start is applied, its release
// requested, and the next recovery retires the execution.
func TestRecoveryAppliesTheReportsDespiteAFailingExitRequest(t *testing.T) {
	ctx := context.Background()
	w := abandonedStart(t)
	w.executions.Conflicts = 3

	err := w.recovery.RecoverWork(ctx, w.works.work.ID)
	require.ErrorIs(t, err, domain.ErrConcurrentModification, "the exit request's failure is reported")
	require.True(t, w.works.work.HarnessProcess.StartAbandoned, "the abandonment is saved")
	require.Equal(t, domain.WorkFailed, w.works.work.State)
	require.False(t, w.execution(t).Retired())

	require.NoError(t, w.recovery.RecoverWork(ctx, w.works.work.ID))
	require.Equal(t, domain.RetiredByAbandonment, w.execution(t).Retirement, "a lost retirement request is repaired by the recovery")
	require.NoError(t, w.recovery.RecoverWork(ctx, w.works.work.ID))
	require.Equal(t, domain.RetiredByAbandonment, w.execution(t).Retirement, "nothing is left to retry")
}

// Each recovery step's failure is reported, and none holds back another.
func TestRecoveryReturnsEveryStepFailureTogether(t *testing.T) {
	w := abandonedStart(t)
	coordinationErr, saveErr := errors.New("execution store unavailable"), errors.New("work store unavailable")
	w.executions.SaveErr = coordinationErr
	w.works.saveErr = saveErr

	err := w.recovery.RecoverWork(context.Background(), w.works.work.ID)
	require.ErrorIs(t, err, coordinationErr)
	require.ErrorIs(t, err, saveErr)
	require.False(t, w.works.work.HarnessProcess.StartAbandoned)
}
