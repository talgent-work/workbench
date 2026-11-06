package execution

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/samber/oops"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/pkg/execd"
	"xorm.io/xorm"
)

// ManagedEnvironment is the Managed realization's environment authority. A
// Managed allocation's environment is the Work's one directory on its
// machine, opened idempotently and kept for the Work's life, so it is never
// duplicated; its compute is the run's processes, whose release the removal
// obligation records. Its Harness uses the machine's own git credentials, and
// the machine's own Claude login or the Work's Model Configuration; the
// Work's credentials (the Work credential for the platform Connector and any
// Model Configuration key) are delivered to Talgent Computer's local proxy,
// which alone carries them (managed_credentials.go).
type ManagedEnvironment struct {
	// db holds the allocations, whose inputs marks this realization keeps.
	db       *xorm.Engine
	machines *ManagedComputers
	// models are the platform's Model Configurations.
	models *ModelCatalog
	// works says whether the allocation still backs its Work's running start,
	// which every delivery checks.
	works *HeldWorks
	// inputs are the Attachments of the Work's input source, which their
	// own sync hands the machine (managed_inputs.go).
	inputs WorkInputFiles
	// inputsRefresh is how long synced inputs stay current before the
	// recovery sweep syncs them again (managed_inputs.go).
	inputsRefresh time.Duration
	// reconciliation reconciles a reconnected machine before anything of
	// its Works runs there (managed_reconciliation.go).
	reconciliation *ManagedReconciliation
	now            func() time.Time
	logger         *slog.Logger
}

var (
	_ environmentAuthority = ManagedEnvironment{}
	_ credentialDelivery   = ManagedEnvironment{}
	_ fencingEnvironment   = ManagedEnvironment{}
)

// NewManagedEnvironment is the Managed realization of a deployment whose
// relay reaches Managed Computers.
func NewManagedEnvironment(db *xorm.Engine, machines *ManagedComputers, models *ModelCatalog, works *HeldWorks, inputs WorkInputFiles, inputsRefresh time.Duration, logger *slog.Logger) (*ManagedEnvironment, error) {
	if db == nil || machines == nil || models == nil || works == nil || inputs == nil {
		return nil, errors.New("agent: allocations, Managed Computers, Model Configurations, Work state and input files are required for the Managed realization")
	}
	if inputsRefresh <= 0 {
		return nil, errors.New("agent: the inputs refresh interval must be positive")
	}
	if logger == nil {
		logger = slog.Default()
	}
	reconciliation, err := NewManagedReconciliation(db, machines, works)
	if err != nil {
		return nil, err
	}
	return &ManagedEnvironment{db: db, machines: machines, models: models, works: works, inputs: inputs, inputsRefresh: inputsRefresh, reconciliation: reconciliation, now: time.Now, logger: logger}, nil
}

// Reconciliation reconciles the Managed Computers this realization runs
// Works on.
func (m *ManagedEnvironment) Reconciliation() *ManagedReconciliation { return m.reconciliation }

func (ManagedEnvironment) launchLayout() launchLayout { return machineLayout }

// specify holds no mounts and no Provider credentials, so the allocation uses
// no authorization the platform would redeliver. It issues the run's own
// telemetry token (managed_telemetry.go).
func (m ManagedEnvironment) specify(ctx context.Context, request domain.StartExecutionRequest, _ domain.SandboxMounts, _ Allocation, token, credential string) (environmentSpecification, error) {
	spec, err := managedSpecificationFor(request, m.models, token, credential)
	if err != nil {
		return environmentSpecification{}, err
	}
	if err := spec.attachTelemetry(ctx, request, m.machines.telemetry, rand.Text()); err != nil {
		return environmentSpecification{}, err
	}
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
// up. The allocation's own identity stands for the environment it opened. A
// machine the relay does not reach now leaves it unresolved; one still
// installing Claude Code does too, and says so.
func (m ManagedEnvironment) create(ctx context.Context, record allocationRecord) (string, error) {
	spec, err := decodeManagedSpecification(record)
	if err != nil {
		return "", err
	}
	err = m.machines.open(ctx, spec, record.SessionID)
	if errors.Is(err, errMachineUnreachable) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return record.ID, nil
}

func (m ManagedEnvironment) find(ctx context.Context, record allocationRecord) (string, error) {
	return m.create(ctx, record)
}

// resolve reaches the environment through the relay with the relay's own
// client, which never follows a redirect with the Work's token.
func (m ManagedEnvironment) resolve(ctx context.Context, record allocationRecord, _ *http.Client) (environment, environmentState, error) {
	spec, err := decodeManagedSpecification(record)
	if err != nil {
		return environment{}, environmentPending, err
	}
	reached, state, err := m.machines.resolve(ctx, spec, record.SessionID)
	if state == environmentLost {
		return environment{}, environmentLost, nil
	}
	if err != nil || state != environmentRunning {
		return environment{}, environmentPending, err
	}
	// Every step that may start a process resolves first: a machine that
	// reconnected starts nothing before its reconciliation, which this
	// performs when its ping reports the round undecided and no Presence
	// fact decided it yet. A decided round costs no further call.
	if !reached.reconciled {
		if err := m.reconciliation.ReconcileComputer(ctx, spec.ComputerID); err != nil {
			return environment{}, environmentPending, err
		}
	}
	client, err := execd.NewClient(m.machines.http, reached.endpoint, spec.AccessToken)
	if err != nil {
		return environment{}, environmentPending, err
	}
	return environment{endpoint: reached.endpoint, token: spec.AccessToken, client: client}, environmentRunning, nil
}

// lost is the removal of the Work's Computer.
func (m ManagedEnvironment) lost(ctx context.Context, record allocationRecord) (bool, error) {
	spec, err := decodeManagedSpecification(record)
	if err != nil {
		return false, err
	}
	return m.machines.removed(ctx, spec)
}

// release deletes the run's process handles on the machine; the Work's
// directory stays. A machine not reachable now keeps the obligation pending.
func (m ManagedEnvironment) release(ctx context.Context, allocation allocationRecord, _ removalRecord) (bool, error) {
	spec, err := decodeManagedSpecification(allocation)
	if err != nil {
		return false, err
	}
	return m.machines.release(ctx, spec, allocation.SessionID, allocation.PreparationProcessID, allocation.ProcessID)
}

// workCredential is the one the Work's credentials deliver to the machine's
// local proxy.
func (ManagedEnvironment) workCredential(record allocationRecord) (string, error) {
	spec, err := decodeManagedSpecification(record)
	return spec.WorkCredential, err
}

// outlived reports that the allocation's Work no longer holds its run.
func (m ManagedEnvironment) outlived(ctx context.Context, record allocationRecord) (bool, error) {
	return m.works.outlived(ctx, record)
}
