package execution

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/samber/oops"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/pkg/execd"
	"xorm.io/xorm"
)

// ManagedEnvironment is the Managed realization's environment authority. A
// Managed execution's environment is the Work's one directory on its
// machine, opened idempotently and kept for the Work's life, so it is never
// duplicated; the execution's own identity stands for it. Its compute is the
// run's processes, whose deletion the execution's record keeps
// (handles_released). Its Harness uses the machine's own git credentials, and
// the machine's own Claude login or the Work's Model Configuration; the
// Work's credentials (the Work credential for the platform Connector and any
// Model Configuration key) are delivered to Talgent Computer's local proxy,
// which alone carries them (managed_credentials.go).
type ManagedEnvironment struct {
	// records are the executions, whose input owner and released handles
	// this realization keeps.
	records  records
	machines *ManagedComputers
	// models are the platform's Model Configurations.
	models *ModelCatalog
	// inputs are the Attachments of the Work's input source, which their
	// own sync hands the machine (managed_inputs.go).
	inputs WorkInputFiles
	// inputsRefresh is how long synced inputs stay current before the
	// recovery sweep syncs them again (managed_inputs.go).
	inputsRefresh time.Duration
	// reconciliation reconciles a reconnected machine before anything of
	// its Works runs there (managed_reconciliation.go).
	reconciliation *ManagedReconciliation
}

var (
	_ environmentAuthority = ManagedEnvironment{}
	_ credentialDelivery   = ManagedEnvironment{}
)

// NewManagedEnvironment is the Managed realization of a deployment whose
// relay reaches Managed Computers.
func NewManagedEnvironment(db *xorm.Engine, machines *ManagedComputers, models *ModelCatalog, sessions domain.AgentWorkRepository, inputs WorkInputFiles, inputsRefresh time.Duration) (*ManagedEnvironment, error) {
	if db == nil || machines == nil || models == nil || sessions == nil || inputs == nil {
		return nil, errors.New("agent: executions, Managed Computers, Model Configurations, Works and input files are required for the Managed realization")
	}
	if inputsRefresh <= 0 {
		return nil, errors.New("agent: the inputs refresh interval must be positive")
	}
	reconciliation, err := NewManagedReconciliation(db, machines, sessions)
	if err != nil {
		return nil, err
	}
	return &ManagedEnvironment{records: records{db: db}, machines: machines, models: models, inputs: inputs, inputsRefresh: inputsRefresh, reconciliation: reconciliation}, nil
}

// Reconciliation reconciles the Managed Computers this realization runs
// Works on.
func (m *ManagedEnvironment) Reconciliation() *ManagedReconciliation { return m.reconciliation }

func (ManagedEnvironment) launchLayout() launchLayout { return machineLayout }

// specify holds no mounts and no Provider credentials, so the execution uses
// no authorization the platform would redeliver. It issues the run's own
// telemetry token (managed_telemetry.go).
func (m ManagedEnvironment) specify(ctx context.Context, request domain.LaunchSpecification, _ domain.SandboxMounts, _ Allocation, token, credential string) (environmentSpecification, error) {
	spec, err := managedSpecificationFor(request, m.models, token, credential)
	if err != nil {
		return environmentSpecification{}, err
	}
	if err := spec.attachTelemetry(ctx, request, m.machines.telemetry, rand.Text()); err != nil {
		return environmentSpecification{}, err
	}
	spec.APIAddress = m.machines.api
	document, err := json.Marshal(spec)
	if err != nil {
		return environmentSpecification{}, oops.Wrap(err)
	}
	return environmentSpecification{document: document, authorizations: []byte("[]")}, nil
}

// claim freezes nothing: the machine's own git credentials serve the Work.
func (ManagedEnvironment) claim(_ context.Context, record allocationRecord) ([]byte, error) {
	_, err := decodeManagedSpecification(record)
	return nil, err
}

// create opens the Work's environment on its machine. Opening is idempotent,
// so a repeated or uncertain attempt opens it again instead of looking it
// up. The execution's own identity stands for the environment it opened. A
// machine the relay does not reach now leaves it unresolved; one still
// installing Claude Code does too, and says so.
func (m ManagedEnvironment) create(ctx context.Context, record allocationRecord) (string, error) {
	spec, err := decodeManagedSpecification(record)
	if err != nil {
		return "", err
	}
	attempt, cancel := context.WithTimeout(ctx, reconciliationAttemptTimeout)
	defer cancel()
	err = m.machines.open(attempt, spec, record.SessionID)
	if errors.Is(err, errMachineUnreachable) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return record.ID, nil
}

// resolve reaches the environment through the relay with the relay's own
// client, which never follows a redirect with the Work's token.
func (m ManagedEnvironment) resolve(ctx context.Context, record allocationRecord, _ *http.Client) (environment, environmentState, error) {
	spec, err := decodeManagedSpecification(record)
	if err != nil {
		return environment{}, environmentPending, err
	}
	reached, state, err := m.resolveMachine(ctx, spec, record.SessionID)
	if err != nil || state != environmentRunning {
		return environment{}, state, err
	}
	client, err := execd.NewClient(m.machines.http, reached.endpoint, spec.AccessToken)
	if err != nil {
		return environment{}, environmentPending, err
	}
	return environment{endpoint: reached.endpoint, token: spec.AccessToken, client: client}, environmentRunning, nil
}

// resolveMachine finds the Work's environment on its machine. Every step
// that may start a process resolves first: a machine that reconnected starts
// nothing before its reconciliation, which this performs when its ping
// reports the round undecided and no Presence fact decided it yet. A decided
// round costs no further call.
func (m ManagedEnvironment) resolveMachine(ctx context.Context, spec managedSpecification, workID string) (reachedWork, environmentState, error) {
	reached, state, err := m.machines.resolve(ctx, spec, workID)
	if state == environmentLost {
		return reachedWork{}, environmentLost, nil
	}
	if err != nil || state != environmentRunning {
		return reachedWork{}, environmentPending, err
	}
	if !reached.reconciled {
		if err := m.reconciliation.ReconcileComputer(ctx, spec.ComputerID); err != nil {
			return reachedWork{}, environmentPending, err
		}
	}
	return reached, environmentRunning, nil
}

// observe finds the execution's environment on its machine. Opening is
// idempotent, so the environment of a submitted creation whose answer was
// lost is found by opening it again; one the machine does not reach now is
// found again later. A bound environment is present, not running, while the
// relay does not reach the machine, and absent once the machine holds nothing
// of the Work or its Computer was removed. A retired execution's environment
// is gone once its run's process handles were deleted: the Work's directory
// is the Work's, not the run's.
func (m ManagedEnvironment) observe(ctx context.Context, record allocationRecord, retired bool) (environmentView, error) {
	spec, err := decodeManagedSpecification(record)
	if err != nil {
		return environmentView{}, err
	}
	if record.SandboxID == "" {
		if !record.Submitted || retired {
			return environmentView{}, nil
		}
		opened, err := m.create(ctx, record)
		if err != nil || opened == "" {
			return environmentView{}, err
		}
		return environmentView{environments: []domain.ObservedEnvironment{{ID: opened, Running: true}}}, nil
	}
	if retired && record.HandlesReleased {
		return environmentView{}, nil
	}
	reached, state, err := m.resolveMachine(ctx, spec, record.SessionID)
	switch {
	case err != nil:
		return environmentView{}, err
	case state == environmentLost:
		removed, err := m.machines.removed(ctx, spec)
		return environmentView{removed: removed}, err
	case state == environmentPending:
		return environmentView{environments: []domain.ObservedEnvironment{{ID: record.SandboxID}}}, nil
	}
	return environmentView{environments: []domain.ObservedEnvironment{{ID: record.SandboxID, Running: true}}, endpoint: reached.endpoint}, nil
}

// remove deletes the run's process handles on the machine and records it;
// the Work's directory stays. A machine not reachable now keeps the
// environment present until it is reachable again.
func (m ManagedEnvironment) remove(ctx context.Context, record allocationRecord, id string) error {
	if id != record.ID {
		return errEnvironmentConflict
	}
	spec, err := decodeManagedSpecification(record)
	if err != nil {
		return err
	}
	released, err := m.machines.release(ctx, spec, record.SessionID, record.PreparationProcessID, record.ProcessID)
	if err != nil || !released {
		return err
	}
	if _, err := m.records.db.Context(ctx).Exec("UPDATE execution_allocation SET handles_released = 1 WHERE id = ?", record.ID); err != nil {
		return oops.With("operation", "record_released_handles").Wrap(err)
	}
	return nil
}

// workCredential is the one the Work's credentials deliver to the machine's
// local proxy.
func (ManagedEnvironment) workCredential(record allocationRecord) (string, error) {
	spec, err := decodeManagedSpecification(record)
	return spec.WorkCredential, err
}
