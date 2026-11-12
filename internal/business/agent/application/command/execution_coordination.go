package command

import (
	"context"
	"errors"
	"time"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// coordinationAttempts bounds retries of a Root changed concurrently.
const coordinationAttempts = 3

// ExecutionCoordination carries the execution requests of a Work to its AgentExecution
// and the execution's reports back, each Root changed in its own
// transaction. Every use case reads both Roots as they are now, so a repeated,
// lost or reordered Domain Event leads to the same outcome, and the Work's
// recovery repairs what a lost event left undone.
type ExecutionCoordination struct {
	now         func() time.Time
	works       domain.AgentWorkRepository
	definitions domain.AgentDefinitionRepository
	executions  domain.AgentExecutionRepository
	computers   ComputerPort
	buffer      DispatcherPort
}

func NewExecutionCoordination(now func() time.Time, works domain.AgentWorkRepository, definitions domain.AgentDefinitionRepository, executions domain.AgentExecutionRepository, computers ComputerPort, buffer DispatcherPort) *ExecutionCoordination {
	return &ExecutionCoordination{now: now, works: works, definitions: definitions, executions: executions, computers: computers, buffer: buffer}
}

// Coordinate applies every report the Work's current execution made, its
// checkout outcomes among them, then passes every request the Work holds, so a request a report caused, such as
// the retirement an exit asks for, is passed in the same sweep; each step
// runs regardless of the others' outcome.
func (c *ExecutionCoordination) Coordinate(ctx context.Context, workID string) error {
	return errors.Join(
		c.ApplyExecutionReports(ctx, workID),
		c.BeginExecution(ctx, workID),
		c.RequestProcessExit(ctx, workID),
		c.RetireExecution(ctx, workID),
	)
}

// BeginExecution begins the AgentExecution of the Work's admitted start
// (Begin execution), with the LaunchSpecification taken from the Work and
// where its Computer realizes it. An earlier execution still unretired, whose
// retirement request was lost, retires first (Superseded execution). A start
// that has its execution already changes nothing, so a concurrent beginning
// that won is taken as done.
func (c *ExecutionCoordination) BeginExecution(ctx context.Context, workID string) error {
	work, err := c.works.Get(ctx, workID)
	if err != nil {
		return err
	}
	admitted, ok := requestOf[domain.WorkStartAdmitted](work)
	if !ok {
		return nil
	}
	for range coordinationAttempts {
		err = c.begin(ctx, work, admitted)
		if !errors.Is(err, domain.ErrConcurrentModification) && !errors.Is(err, domain.ErrExecutionAdmission) {
			return err
		}
	}
	return err
}

// begin makes one attempt to supersede the Work's earlier execution and
// begin the admitted start's own.
func (c *ExecutionCoordination) begin(ctx context.Context, work *domain.AgentWork, admitted domain.WorkStartAdmitted) error {
	current, err := c.latest(ctx, work.ID)
	if err != nil || (current != nil && current.Serves(admitted.StartAdmittedAt)) {
		return err
	}
	if current != nil {
		superseded, err := current.Retire(admitted, c.now())
		if err != nil {
			return err
		}
		if superseded {
			if err := c.executions.Save(ctx, current); err != nil {
				return err
			}
		}
	}
	place, err := c.computers.DescribeComputer(ctx, work.ComputerID)
	if err != nil {
		return err
	}
	definition, err := c.definitions.Get(ctx, work.AgentID)
	if err != nil {
		return err
	}
	launch := work.LaunchSpecification()
	launch.ComputerName, launch.Purpose, launch.OwningApplication = place.Name, definition.Purpose, definition.OwningApplication()
	realization := domain.RealizationCloud
	if work.OnManagedComputer() {
		realization = domain.RealizationManaged
	}
	execution, err := domain.BeginExecution(domain.ExecutionBeginning{Admitted: admitted, Launch: launch, Realization: realization})
	if err != nil {
		return err
	}
	return c.executions.Save(ctx, execution)
}

// RequestProcessExit records the Work's request that its process exit on the
// current execution (Request process exit).
func (c *ExecutionCoordination) RequestProcessExit(ctx context.Context, workID string) error {
	return c.passRequest(ctx, workID, func(work *domain.AgentWork, execution *domain.AgentExecution) (bool, error) {
		requested, ok := requestOf[domain.WorkStopRequested](work)
		if !ok {
			return false, nil
		}
		return execution.RequestExit(requested)
	})
}

// RetireExecution retires the current execution once its Work's execution
// ended (Retire execution).
func (c *ExecutionCoordination) RetireExecution(ctx context.Context, workID string) error {
	return c.passRequest(ctx, workID, func(work *domain.AgentWork, execution *domain.AgentExecution) (bool, error) {
		requested, ok := requestOf[domain.WorkComputeReleaseRequested](work)
		if !ok {
			return false, nil
		}
		return execution.Retire(requested, c.now())
	})
}

func (c *ExecutionCoordination) passRequest(ctx context.Context, workID string, pass func(*domain.AgentWork, *domain.AgentExecution) (bool, error)) error {
	var err error
	for range coordinationAttempts {
		var work *domain.AgentWork
		if work, err = c.works.Get(ctx, workID); err != nil {
			return err
		}
		var execution *domain.AgentExecution
		if execution, err = c.latest(ctx, workID); err != nil || execution == nil {
			return err
		}
		var changed bool
		if changed, err = pass(work, execution); err != nil || !changed {
			return err
		}
		if err = c.executions.Save(ctx, execution); !errors.Is(err, domain.ErrConcurrentModification) {
			return err
		}
	}
	return err
}

// RecordCheckouts records the checkout outcomes the Work's current execution
// reported on its Work (Record checkouts).
func (c *ExecutionCoordination) RecordCheckouts(ctx context.Context, workID string) error {
	return c.applyExecution(ctx, workID, checkoutsOnly)
}

// ApplyExecutionReports applies every report the Work's current execution
// made, in order (Apply execution report, Confirm compute release), after
// its checkout outcomes, so a Work whose release is confirmed holds the
// outcomes its next start leaves out. A Work that became ready has its
// idleness recorded from its input buffer.
func (c *ExecutionCoordination) ApplyExecutionReports(ctx context.Context, workID string) error {
	return c.applyExecution(ctx, workID, withReports)
}

// executionFacts names what applyExecution applies of the execution: its
// checkout outcomes alone, or those and then every report.
type executionFacts int

const (
	checkoutsOnly executionFacts = iota
	withReports
)

func (c *ExecutionCoordination) applyExecution(ctx context.Context, workID string, facts executionFacts) error {
	var err error
	for range coordinationAttempts {
		var execution *domain.AgentExecution
		if execution, err = c.latest(ctx, workID); err != nil || execution == nil {
			return err
		}
		var work *domain.AgentWork
		if work, err = c.works.Get(ctx, workID); err != nil {
			return err
		}
		wasStarting := work.State == domain.WorkStarting
		changed := false
		if ended, ok := execution.CheckoutsEnded(); ok {
			changed = work.RecordRepositoryCheckouts(ended)
		}
		if facts == withReports {
			for _, report := range execution.Reports() {
				applied, applyErr := work.ApplyExecutionReport(report)
				if applyErr != nil {
					return applyErr
				}
				changed = changed || applied
			}
		}
		if !changed {
			return nil
		}
		if wasStarting && work.State == domain.WorkWorking {
			if err = c.recordIdleness(ctx, work); err != nil {
				return err
			}
		}
		if err = c.works.Save(ctx, work); !errors.Is(err, domain.ErrConcurrentModification) {
			return err
		}
	}
	return err
}

// recordIdleness supplies the accepted-input fact to a Work that just became
// ready; the Work decides whether nothing is left to process.
func (c *ExecutionCoordination) recordIdleness(ctx context.Context, work *domain.AgentWork) error {
	buffered, err := c.buffer.ReadBuffer(ctx, work.ID)
	if err != nil {
		return err
	}
	return work.RecordIdleness(buffered.Pending(), c.now())
}

// latest is the Work's most recently begun execution, nil when it has none.
func (c *ExecutionCoordination) latest(ctx context.Context, workID string) (*domain.AgentExecution, error) {
	execution, err := c.executions.Latest(ctx, workID)
	if errors.Is(err, domain.ErrExecutionNotFound) {
		return nil, nil
	}
	return execution, err
}

// requestOf picks the Work's current request of one kind.
func requestOf[T domain.Event](work *domain.AgentWork) (T, bool) {
	for _, request := range work.ExecutionRequests() {
		if typed, ok := request.(T); ok {
			return typed, true
		}
	}
	var none T
	return none, false
}

// startQueued is the supplied fact that the Work's current start waits for
// its Desk: its execution is Queued, or, not begun yet, its Computer admitted
// it waiting.
func startQueued(ctx context.Context, executions domain.AgentExecutionRepository, work *domain.AgentWork) (bool, error) {
	admitted, ok := requestOf[domain.WorkStartAdmitted](work)
	if !ok {
		return false, nil
	}
	execution, err := executions.Latest(ctx, work.ID)
	if errors.Is(err, domain.ErrExecutionNotFound) || (err == nil && !execution.Serves(admitted.StartAdmittedAt)) {
		return admitted.Waiting, nil
	}
	if err != nil {
		return false, err
	}
	return execution.Queued(), nil
}
