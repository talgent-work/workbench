package command_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"
)

// fixtureComputer is the Computer every fixture Work runs on.
const fixtureComputer = "01a19bc3-075a-7c57-b42b-7fdf73b611ed"

// world is one Work with its executions and the ports its use cases reach,
// with a clock a test may move.
type world struct {
	works        *feedbackSessionStore
	executions   *commandtest.Executions
	harness      *commandtest.Harness
	computers    *commandtest.Computers
	environment  *commandtest.Environment
	storage      *commandtest.Storage
	settlements  *commandtest.InputSettlements
	coordination *command.ExecutionCoordination
	// lease is the platform's configured lease terms; zero leases nothing.
	lease domain.LeaseTerms
	at    time.Time
}

func newWorld(t *testing.T, work *domain.AgentWork) *world {
	t.Helper()
	w := &world{works: &feedbackSessionStore{session: work}, executions: &commandtest.Executions{}, harness: commandtest.NewHarness(),
		computers: &commandtest.Computers{}, environment: &commandtest.Environment{}, storage: &commandtest.Storage{}, settlements: &commandtest.InputSettlements{}, at: time.Now()}
	w.coordination = command.NewExecutionCoordination(w.now, w.works, w.executions, w.computers, w.harness)
	return w
}

// worldOver is a world over a Work store a test already holds.
func worldOver(t *testing.T, works *feedbackSessionStore) *world {
	t.Helper()
	w := newWorld(t, works.session)
	w.works = works
	w.coordination = command.NewExecutionCoordination(w.now, works, w.executions, w.computers, w.harness)
	return w
}

func (w *world) now() time.Time { return w.at }

// advance moves the clock.
func (w *world) advance(d time.Duration) { w.at = w.at.Add(d) }

func (w *world) work() *domain.AgentWork { return w.works.session }

func (w *world) feedback() *command.ApplyExecutionFeedbackHandler {
	return command.NewApplyExecutionFeedbackHandler(w.now, w.works, w.executions, w.coordination, w.harness, w.settlements)
}

func (w *world) reconciliation() *command.ReconcileExecutionHandler {
	return command.NewReconcileExecutionHandler(w.now, w.lease, commandtest.CreationWaits, w.executions, w.environment, w.harness, w.computers, w.storage)
}

// execution is the Work's most recently begun execution.
func (w *world) execution(t *testing.T) *domain.AgentExecution {
	t.Helper()
	execution, err := w.executions.Latest(context.Background(), w.work().ID)
	require.NoError(t, err)
	return execution
}

// begin begins the execution of the Work's admitted start.
func (w *world) begin(t *testing.T) *domain.AgentExecution {
	t.Helper()
	require.NoError(t, w.coordination.BeginExecution(context.Background(), w.work().ID))
	return w.execution(t)
}

// change applies a change to the current execution and saves it.
func (w *world) change(t *testing.T, apply func(*domain.AgentExecution)) {
	t.Helper()
	execution := w.execution(t)
	apply(execution)
	require.NoError(t, w.executions.Save(context.Background(), execution))
}

// launch begins the execution, binds and delivers its environment and records
// its launched process.
func (w *world) launch(t *testing.T, processID string) {
	t.Helper()
	w.begin(t)
	w.change(t, func(execution *domain.AgentExecution) {
		require.True(t, commandtest.Submit(execution))
		_, err := execution.RecordCreationAnswer(domain.CreationAnswer{EnvironmentID: "environment"})
		require.NoError(t, err)
		require.True(t, execution.RecordCredentialDelivery(domain.CredentialDelivery{}))
		prepared, err := execution.RecordPreparation(domain.PreparationReport{})
		require.NoError(t, err)
		require.True(t, prepared)
		launched, err := execution.RecordLaunch(processID)
		require.NoError(t, err)
		require.True(t, launched)
	})
}

// ready launches "process" and reports it ready, applying the report to the
// Work.
func (w *world) ready(t *testing.T) {
	t.Helper()
	w.launch(t, "process")
	w.change(t, func(execution *domain.AgentExecution) {
		_, err := execution.ConfirmReady(domain.ReadinessReport{Prepared: true, Started: true}, w.now())
		require.NoError(t, err)
	})
	require.NoError(t, w.coordination.ApplyExecutionReports(context.Background(), w.work().ID))
	require.Equal(t, domain.WorkWorking, w.work().State)
}

// stop stops the Work and passes its request that the process exit.
func (w *world) stop(t *testing.T, requestID string) {
	t.Helper()
	require.NoError(t, w.work().Stop(requestID, false))
	require.NoError(t, w.works.Save(context.Background(), w.work()))
	require.NoError(t, w.coordination.RequestProcessExit(context.Background(), w.work().ID))
}

// exit reports the launched process's exit through Dispatcher's feedback and
// retires the execution once its Work asks.
func (w *world) exit(t *testing.T, code int) {
	t.Helper()
	require.NoError(t, w.feedback().Handle(context.Background(), command.ExecutionFeedback{Kind: command.ExecutionExited, WorkID: w.work().ID, ProcessID: w.execution(t).Process.ID, ExitCode: code}))
	require.NoError(t, w.coordination.RetireExecution(context.Background(), w.work().ID))
}

// release confirms a retired execution's release once nothing is observed,
// and applies it to the Work.
func (w *world) release(t *testing.T) {
	t.Helper()
	w.environment.Observation = domain.EnvironmentObservation{}
	_, err := w.reconciliation().Reconcile(context.Background(), w.execution(t).ID)
	require.NoError(t, err)
	require.Equal(t, domain.ExecutionReleased, w.execution(t).State)
	require.NoError(t, w.coordination.ApplyExecutionReports(context.Background(), w.work().ID))
}

func definitionFixture(t *testing.T) *domain.AgentDefinition {
	t.Helper()
	agent, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"owner"}, AgentName: "Fixture Agent", Purpose: "work", UsageScope: domain.UsageScope{Kind: "employee", Reference: "employee"}, Capabilities: domain.CapabilityConfiguration{}})
	require.NoError(t, err)
	return agent
}

// fixtureAdmission admits a seated start of the definition's Work now.
//
// It was admitted a minute ago: starts are told apart at millisecond
// precision, as persisted, so a later start a test admits now never shares
// its admission time.
func fixtureAdmission(agentID string) domain.StartAdmission {
	return domain.StartAdmission{AdmittedAt: time.Now().Add(-time.Minute), AgentID: agentID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: fixtureComputer, Kind: domain.ComputerCloud, DeskID: "desk-test"}, ModelConfigurationProvided: true}
}

// nextAdmission admits a later start of the Work under a new request, which
// derives a Desk of its own.
func nextAdmission(agentID string) domain.StartAdmission {
	admission := fixtureAdmission(agentID)
	admission.Seat.DeskID = uuid.NewString()
	return admission
}

// startingSession is a new Work whose first start is admitted and seated.
func startingSession(t *testing.T) *domain.AgentWork {
	t.Helper()
	return admittedSession(t, false)
}

// admittedSession is a new Work whose first start is admitted, seated or,
// when waiting, in its Computer's line.
func admittedSession(t *testing.T, waiting bool) *domain.AgentWork {
	t.Helper()
	agent := definitionFixture(t)
	admission := fixtureAdmission(agent.ID)
	admission.Seat.Waiting = waiting
	session, err := domain.NewAgentWork(domain.WorkInput{Subject: domain.WorkSubject{Kind: "case", ID: "case"}, ProjectID: "project", WorkModel: domain.WorkModelSelection{ConfigurationID: "default"}, StartedBy: "owner", Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}}, ID: uuid.Must(uuid.NewV7()).String(), RepresentedPerson: "user-home", WorkLayout: domain.DefaultWorkLayout(), AgentID: agent.ID}, admission)
	require.NoError(t, err)
	return settled(session)
}

// settled is a fixture Work whose Domain Events were committed already, so a
// test sees only those its own use case records.
func settled(work *domain.AgentWork) *domain.AgentWork {
	work.Events.Drain()
	return work
}

// report applies an execution report of the Work's current start.
func report(t *testing.T, work *domain.AgentWork, build func(workID string, start time.Time) domain.Event) {
	t.Helper()
	_, err := work.ApplyExecutionReport(build(work.ID, work.HarnessProcess.StartingSince))
	require.NoError(t, err)
}

func readyReport(workID string, start time.Time) domain.Event {
	return domain.ExecutionReady{WorkID: workID, StartAdmittedAt: start}
}

func exitedReport(workID string, start time.Time) domain.Event {
	return domain.ExecutionExited{WorkID: workID, StartAdmittedAt: start, Ready: true}
}

func releasedReport(workID string, start time.Time) domain.Event {
	return domain.ExecutionReleasedEvent{WorkID: workID, StartAdmittedAt: start}
}

// runningSession is a Work whose run became ready.
func runningSession(t *testing.T) *domain.AgentWork {
	t.Helper()
	session := startingSession(t)
	report(t, session, readyReport)
	require.Equal(t, domain.WorkWorking, session.State)
	return settled(session)
}

// stoppingSession is a running Work whose stop is admitted.
func stoppingSession(t *testing.T) *domain.AgentWork {
	t.Helper()
	session := runningSession(t)
	require.NoError(t, session.Stop("stop", false))
	return settled(session)
}

// stoppedSession is a Work whose run exited after its stop and whose compute
// was released.
func stoppedSession(t *testing.T) *domain.AgentWork {
	t.Helper()
	session := stoppingSession(t)
	report(t, session, exitedReport)
	report(t, session, releasedReport)
	require.Equal(t, domain.WorkStopped, session.State)
	return settled(session)
}

// executionRequests lists the kinds of the execution requests the Work holds.
func executionRequests(work *domain.AgentWork) []domain.EventKind {
	var kinds []domain.EventKind
	for _, request := range work.ExecutionRequests() {
		kinds = append(kinds, request.Kind())
	}
	return kinds
}
