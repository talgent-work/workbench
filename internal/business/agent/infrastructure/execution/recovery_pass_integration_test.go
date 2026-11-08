//go:build integration

package execution

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"xorm.io/xorm"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
)

// register begins the execution of the start launch describes, seated on a
// Cloud, and fixes its environment specification with mounts, as its first
// reconciliation does before submitting the creation. It returns the
// execution's record.
func register(ctx context.Context, p *Provisioning, launch domain.LaunchSpecification, mounts domain.SandboxMounts) (executionRecord, error) {
	return registerAs(ctx, p, launch, mounts, domain.RealizationCloud)
}

// registerAs is register on a Computer of realization.
func registerAs(ctx context.Context, p *Provisioning, launch domain.LaunchSpecification, mounts domain.SandboxMounts, realization domain.ExecutionRealization) (executionRecord, error) {
	run, err := domain.BeginExecution(domain.ExecutionBeginning{
		Admitted: domain.WorkStartAdmitted{WorkID: launch.WorkID, StartAdmittedAt: p.now(), DeskID: "desk-" + uuid.NewString()},
		Launch:   launch, Realization: realization,
	})
	if err != nil {
		return executionRecord{}, err
	}
	if err := NewAgentExecutionRepository(p.db, p.now).Save(ctx, run); err != nil {
		return executionRecord{}, err
	}
	record, _, err := p.get(ctx, run.ID)
	if err != nil {
		return record, err
	}
	if err := p.specify(ctx, record, command.CreationSupply{Mounts: mounts}); err != nil {
		return record, err
	}
	record, _, err = p.get(ctx, run.ID)
	return record, err
}

// executionSteps reconciles executions and takes the steps each decides, as
// the recovery tasks the sweep and their wakes start would.
type executionSteps struct {
	environment *ExecutionEnvironment
	executions  *AgentExecutionRepository
	handler     *command.ReconcileExecutionHandler
	desks       *cloudDesks
	harness     *commandtest.Harness
}

func newExecutionSteps(t testing.TB, p *Provisioning, connections *NativeConnections, owners StreamOwners, managed *ManagedEnvironment, now func() time.Time) *executionSteps {
	t.Helper()
	return newExecutionStepsWith(t, p, connections, owners, managed, now, directoryMounts{root: t.TempDir()})
}

// newExecutionStepsWith acquires a Cloud start's mounts from mounts.
func newExecutionStepsWith(t testing.TB, p *Provisioning, connections *NativeConnections, owners StreamOwners, managed *ManagedEnvironment, now func() time.Time, mounts command.StoragePort) *executionSteps {
	t.Helper()
	environment, err := NewExecutionEnvironment(p, connections, owners, managed)
	require.NoError(t, err)
	executions := NewAgentExecutionRepository(p.db, now)
	steps := &executionSteps{environment: environment, executions: executions, desks: &cloudDesks{}, harness: commandtest.NewHarness()}
	steps.handler = command.NewReconcileExecutionHandler(now, fixtureLease, fixtureCreationWaits, executions, environment, steps.harness, steps.desks, commandtest.Supplies(mounts))
	return steps
}

// reconcile reconciles one execution and takes the long step it decided,
// though a short step failed, as its long step task would. A creation it
// submitted wakes the reconciliation again, as the commit of its submission
// and answer does, and so the steps that follow.
func (s *executionSteps) reconcile(ctx context.Context, id string) error {
	before, err := s.submitted(ctx, id)
	if err != nil {
		return err
	}
	err = s.once(ctx, id)
	after, lookupErr := s.submitted(ctx, id)
	if before || !after || lookupErr != nil {
		return errors.Join(err, lookupErr)
	}
	return errors.Join(err, s.once(ctx, id))
}

func (s *executionSteps) once(ctx context.Context, id string) error {
	long, err := s.handler.Reconcile(ctx, id)
	if !long {
		return err
	}
	return errors.Join(err, s.handler.RunLongStep(ctx, id))
}

// submitted reports whether the execution recorded its creation's
// submission.
func (s *executionSteps) submitted(ctx context.Context, id string) (bool, error) {
	execution, err := s.executions.Get(ctx, id)
	if errors.Is(err, domain.ErrExecutionNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return execution.Creation.Submitted(), nil
}

// shortSteps reconciles one execution, taking only its short steps, as its
// Reconcile execution task does.
func (s *executionSteps) shortSteps(ctx context.Context, id string) error {
	_, err := s.handler.Reconcile(ctx, id)
	return err
}

// pass reconciles every outstanding execution once, as one sweep would.
func (s *executionSteps) pass(ctx context.Context, db *xorm.Engine) error {
	ids, err := infrastructure.NewExecutionQueryRepository(db).ListOutstandingExecutions(ctx, "", 1000)
	if err != nil {
		return err
	}
	var failures []error
	for _, id := range ids {
		failures = append(failures, s.reconcile(ctx, id))
	}
	return errors.Join(failures...)
}

// retire retires the Work's latest execution as its Work asks once its
// execution ended by cause.
func (s *executionSteps) retire(ctx context.Context, workID string, cause domain.RetirementCause, now time.Time) error {
	run, err := s.executions.Latest(ctx, workID)
	if err != nil {
		return err
	}
	changed, err := run.Retire(domain.WorkComputeReleaseRequested{WorkID: workID, StartAdmittedAt: run.StartAdmittedAt, Cause: cause}, now)
	if err != nil || !changed {
		return err
	}
	return s.executions.Save(ctx, run)
}

// reported tells whether the execution reported event to its Work.
func reported(run *domain.AgentExecution, event domain.Event) bool {
	return slices.Contains(run.Reports(), event)
}

// unread is a Dispatcher that reads no Work's stream.
type unread struct{}

func (unread) Reading(context.Context, string) (StreamReading, bool, error) {
	return StreamReading{}, false, nil
}

// unreleased lists retired executions whose release is not confirmed yet.
func unreleased(ctx context.Context, db *xorm.Engine) ([]executionRecord, error) {
	var records []executionRecord
	err := db.Context(ctx).Where("deleted_at <> 0 AND released_at = 0").Asc("id").Find(&records)
	return records, err
}
