package execution

import (
	"context"
	"errors"

	"github.com/samber/oops"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// StreamReading is the process, and its endpoint, whose stream Dispatcher has
// recorded for a Work.
type StreamReading struct {
	ProcessID, Endpoint string
}

// StreamOwners reads which Harness process Dispatcher reads for a Work, so a
// connected execution whose stream is read is observed without asking its
// environment authority.
type StreamOwners interface {
	Reading(ctx context.Context, workID string) (StreamReading, bool, error)
}

// ExecutionEnvironment realizes the execution environment port for every
// Computer alike: it observes an execution's environments through the
// authority of its realization, and takes the steps the execution decides,
// writing only the technical columns of its record. A Managed Computer that
// is reachable again is reconciled by the Managed realization, absent where
// no Managed Computer is reached.
type ExecutionEnvironment struct {
	provisioning *Provisioning
	connections  *NativeConnections
	owners       StreamOwners
	managed      *ManagedReconciliation
}

var _ command.ExecutionEnvironmentPort = (*ExecutionEnvironment)(nil)

// NewExecutionEnvironment takes managed nil for a deployment that reaches no
// Managed Computer.
func NewExecutionEnvironment(provisioning *Provisioning, connections *NativeConnections, owners StreamOwners, managed *ManagedEnvironment) (*ExecutionEnvironment, error) {
	if provisioning == nil || connections == nil || owners == nil {
		return nil, errors.New("agent: provisioning, native connections and stream owners are required for execution environments")
	}
	environment := &ExecutionEnvironment{provisioning: provisioning, connections: connections, owners: owners}
	if managed != nil {
		environment.managed = managed.Reconciliation()
	}
	return environment, nil
}

// errCreationUnresolved means a submitted creation's answer is not known;
// observing the execution's environments resolves it.
var errCreationUnresolved = errors.New("agent: the environment creation's answer is not known yet")

// record reads the execution's record.
func (e *ExecutionEnvironment) record(ctx context.Context, execution *domain.AgentExecution) (allocationRecord, error) {
	record, found, err := e.provisioning.get(ctx, execution.ID)
	if err != nil {
		return record, withExecution(err, execution)
	}
	if !found {
		return record, domain.ErrExecutionNotFound
	}
	return record, nil
}

// Observe shows the execution's environments as its authority lists them, and
// whether Dispatcher reads its launched process at the bound one. Unless
// full, a running process whose stream Dispatcher reads is observed from that
// alone. An execution whose environment was never specified submitted no
// creation, so nothing carries its ownership.
func (e *ExecutionEnvironment) Observe(ctx context.Context, execution *domain.AgentExecution, full bool) (domain.EnvironmentObservation, error) {
	record, err := e.record(ctx, execution)
	if err != nil || !record.specified() {
		return domain.EnvironmentObservation{}, err
	}
	authority, err := e.provisioning.authority(record)
	if err != nil {
		return domain.EnvironmentObservation{}, withExecution(err, execution)
	}
	var observation domain.EnvironmentObservation
	reading, read, err := e.reading(ctx, execution)
	if err != nil {
		return observation, err
	}
	if read && !full {
		observation.Environments = []domain.ObservedEnvironment{{ID: execution.Environment, Running: true}}
		observation.Attached = true
	} else {
		attempt, cancel := context.WithTimeout(ctx, e.provisioning.attemptTimeout)
		view, err := authority.observe(attempt, record, execution.Retired())
		cancel()
		if err != nil {
			return domain.EnvironmentObservation{}, withExecution(err, execution)
		}
		observation.Environments, observation.Removed = view.environments, view.removed
		if view.removed {
			observation.LossReason = domain.ComputerRemovedReason
		}
		observation.Attached = read && view.endpoint != "" && reading.Endpoint == view.endpoint
	}
	if delivery, delivers := authority.(credentialDelivery); delivers {
		if observation.ModelDigest, err = delivery.modelDigest(record); err != nil {
			return domain.EnvironmentObservation{}, withExecution(err, execution)
		}
	}
	if inputs, syncs := authority.(inputDelivery); syncs {
		observation.InputsRefresh = inputs.refresh()
	}
	return observation, nil
}

// reading reports whether Dispatcher reads the execution's launched process,
// and where.
func (e *ExecutionEnvironment) reading(ctx context.Context, execution *domain.AgentExecution) (StreamReading, bool, error) {
	if execution.Retired() || execution.Environment == "" || execution.Process.ID == "" || execution.Process.Exited {
		return StreamReading{}, false, nil
	}
	reading, found, err := e.owners.Reading(ctx, execution.WorkID)
	if err != nil {
		return reading, false, withExecution(err, execution)
	}
	return reading, found && reading.ProcessID == execution.Process.ID, nil
}

// PrepareCreation fixes the execution's environment specification once,
// before its creation is submitted.
func (e *ExecutionEnvironment) PrepareCreation(ctx context.Context, execution *domain.AgentExecution, mounts domain.SandboxMounts) error {
	record, err := e.record(ctx, execution)
	if err != nil {
		return err
	}
	return withExecution(e.provisioning.specify(ctx, record, mounts), execution)
}

// SubmitCreation submits the specified environment's creation once and
// answers what the authority answered: the created environment, or its
// refusal. An answer not known now is errCreationUnresolved.
func (e *ExecutionEnvironment) SubmitCreation(ctx context.Context, execution *domain.AgentExecution) (domain.CreationAnswer, error) {
	record, err := e.record(ctx, execution)
	if err != nil {
		return domain.CreationAnswer{}, err
	}
	if !record.specified() {
		return domain.CreationAnswer{}, withExecution(domain.ErrExecutionRequest, execution)
	}
	authority, err := e.provisioning.authority(record)
	if err != nil {
		return domain.CreationAnswer{}, withExecution(err, execution)
	}
	attempt, cancel := context.WithTimeout(ctx, e.provisioning.attemptTimeout)
	defer cancel()
	id, err := authority.create(attempt, record)
	switch {
	case errors.Is(err, errCreationRefused):
		return e.settleRefusal(attempt, execution, authority, record, err)
	case err != nil:
		return domain.CreationAnswer{}, withExecution(err, execution)
	case id == "":
		return domain.CreationAnswer{}, withExecution(errCreationUnresolved, execution)
	}
	return domain.CreationAnswer{EnvironmentID: id}, nil
}

// settleRefusal answers a refused creation. A refusal means nothing was
// created, so the start is rejected; an environment the authority still shows
// was created regardless and is answered instead, so no compute is ever
// judged absent while it exists.
func (e *ExecutionEnvironment) settleRefusal(ctx context.Context, execution *domain.AgentExecution, authority environmentAuthority, record allocationRecord, refusal error) (domain.CreationAnswer, error) {
	view, err := authority.observe(ctx, record, false)
	if err != nil {
		return domain.CreationAnswer{}, withExecution(errors.Join(refusal, err), execution)
	}
	if len(view.environments) > 0 {
		return domain.CreationAnswer{EnvironmentID: view.environments[0].ID}, nil
	}
	return domain.CreationAnswer{Refused: true}, nil
}

// DeleteEnvironment deletes one environment carrying the execution's
// ownership.
func (e *ExecutionEnvironment) DeleteEnvironment(ctx context.Context, execution *domain.AgentExecution, environmentID string) error {
	record, err := e.record(ctx, execution)
	if err != nil {
		return err
	}
	authority, err := e.provisioning.authority(record)
	if err != nil {
		return withExecution(err, execution)
	}
	attempt, cancel := context.WithTimeout(ctx, e.provisioning.attemptTimeout)
	defer cancel()
	return withExecution(authority.remove(attempt, record, environmentID), execution)
}

// DeliverCredentials delivers the Work's current credentials into the bound
// environment, replacing those delivered before.
func (e *ExecutionEnvironment) DeliverCredentials(ctx context.Context, execution *domain.AgentExecution) (domain.CredentialDelivery, error) {
	record, err := e.record(ctx, execution)
	if err != nil {
		return domain.CredentialDelivery{}, err
	}
	attempt, cancel := context.WithTimeout(ctx, e.provisioning.attemptTimeout)
	defer cancel()
	delivery, err := e.connections.deliver(attempt, record, execution.Credentials.Delivered)
	if !execution.Credentials.Delivered {
		err = e.recovering(ctx, record, err)
	}
	return delivery, withExecution(err, execution)
}

// WithdrawCredentials clears the bound environment's delivered credentials.
func (e *ExecutionEnvironment) WithdrawCredentials(ctx context.Context, execution *domain.AgentExecution) error {
	record, err := e.record(ctx, execution)
	if err != nil {
		return err
	}
	attempt, cancel := context.WithTimeout(ctx, e.provisioning.attemptTimeout)
	defer cancel()
	return withExecution(e.connections.withdraw(attempt, record), execution)
}

// Prepare sees the in-environment preparation through to its exit, bounded by
// ctx, its checkout outcomes recorded, and reports its Plugins' installation.
func (e *ExecutionEnvironment) Prepare(ctx context.Context, execution *domain.AgentExecution) ([]domain.PluginInstallation, error) {
	record, err := e.record(ctx, execution)
	if err != nil {
		return nil, err
	}
	installations, err := e.connections.prepare(ctx, record)
	if errors.Is(err, context.DeadlineExceeded) && ctx.Err() != nil {
		// The bound passed while it prepared: the execution decides what
		// follows, not the recovery flag.
		return nil, err
	}
	if err = withExecution(e.recovering(ctx, record, err), execution); err != nil {
		return nil, err
	}
	return installations, nil
}

// Launch starts the Harness in the bound environment and returns its process.
func (e *ExecutionEnvironment) Launch(ctx context.Context, execution *domain.AgentExecution) (string, error) {
	record, err := e.record(ctx, execution)
	if err != nil {
		return "", err
	}
	id, err := e.connections.launch(ctx, record)
	return id, withExecution(e.recovering(ctx, record, err), execution)
}

// Connect hands the launched process to Dispatcher.
func (e *ExecutionEnvironment) Connect(ctx context.Context, execution *domain.AgentExecution) error {
	record, err := e.record(ctx, execution)
	if err != nil {
		return err
	}
	if record.ProcessID == "" || record.ProcessID != execution.Process.ID {
		return withExecution(errEnvironmentConflict, execution)
	}
	attempt, cancel := context.WithTimeout(ctx, e.provisioning.attemptTimeout)
	defer cancel()
	return withExecution(e.recovering(ctx, record, e.connections.connect(attempt, record)), execution)
}

// SyncInputs hands a Managed Computer the current inputs of the execution's
// Work and answers the input changes the sync covers: those the execution
// had marked when it was read.
func (e *ExecutionEnvironment) SyncInputs(ctx context.Context, execution *domain.AgentExecution) (int64, error) {
	record, err := e.record(ctx, execution)
	if err != nil {
		return 0, err
	}
	authority, err := e.provisioning.authority(record)
	if err != nil {
		return 0, withExecution(err, execution)
	}
	inputs, syncs := authority.(inputDelivery)
	if !syncs {
		return 0, nil
	}
	attempt, cancel := context.WithTimeout(ctx, e.provisioning.attemptTimeout)
	defer cancel()
	err = e.connections.credentials.withLock(attempt, record.SandboxID, func() error {
		current, err := e.provisioning.holding(attempt, record)
		if err != nil {
			return err
		}
		return inputs.syncInputs(attempt, current)
	})
	if err != nil {
		return 0, withExecution(err, execution)
	}
	return execution.Inputs.Due, nil
}

// ReconcileComputer reconciles a Managed Computer that is reachable again;
// without Managed Computers there is none to reconcile.
func (e *ExecutionEnvironment) ReconcileComputer(ctx context.Context, computerID string) error {
	if e.managed == nil {
		return nil
	}
	return e.managed.ReconcileComputer(ctx, computerID)
}

// recovering records whether a startup step's latest attempt left the start
// recovering, which its startup progress shows. A conflict means another step
// changed the execution and says nothing about recovery.
func (e *ExecutionEnvironment) recovering(ctx context.Context, record allocationRecord, err error) error {
	if errors.Is(err, errEnvironmentConflict) || errors.Is(err, errAllocationNotHeld) {
		return err
	}
	return errors.Join(err, e.provisioning.recordRecovery(ctx, record, err != nil))
}

func withExecution(err error, execution *domain.AgentExecution) error {
	if err == nil {
		return nil
	}
	return oops.With("execution_id", execution.ID, "agent_work_id", execution.WorkID).Wrap(err)
}
