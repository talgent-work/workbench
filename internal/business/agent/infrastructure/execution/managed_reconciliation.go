package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"

	"github.com/samber/oops"
	"xorm.io/xorm"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// ManagedReconciliation reconciles a Managed Computer once it is reachable
// again. While the machine was away, Agent may have stopped or
// closed Works whose processes still run there, its fence not having fired,
// and seated new runs in the room they held. Each connection of the machine
// opens a round in which it reserves and starts nothing; Agent, which alone
// knows which Works still run there, reads what the machine holds and
// decides: a Work gone from it (Closed, unknown to Agent or of another
// Computer) has its processes end and its files go; every other process ends
// unless it belongs to the run its Work still holds there, and a stopping or
// stopped Work keeps no exited process, which an attach would run again. The
// machine then opens the round, so a leftover process has always ended
// before anything new starts.
//
// Each decision is made from what the machine reported in the same round,
// while nothing new could be reserved, and names exact processes and Works,
// so concurrent reconciliations of the same round on several replicas agree
// or repeat each other harmlessly, and a decision for an earlier round is
// refused by the machine.
type ManagedReconciliation struct {
	db       *xorm.Engine
	machines *ManagedComputers
	works    *HeldWorks
}

func NewManagedReconciliation(db *xorm.Engine, machines *ManagedComputers, works *HeldWorks) (*ManagedReconciliation, error) {
	if db == nil || machines == nil || works == nil {
		return nil, errors.New("agent: allocations, Managed Computers and Work state are required for reconciliation")
	}
	return &ManagedReconciliation{db: db, machines: machines, works: works}, nil
}

// reconcileAttempts bounds the rounds one reconciliation follows when the
// machine reconnects meanwhile.
const reconcileAttempts = 3

// ReconcileComputer reconciles the machine's current round unless it is
// reconciled already. A machine the relay does not reach now has nothing to
// reconcile until it connects, which opens a new round. A machine that does
// not answer the round is below the Minimum Version and fails the call.
func (r *ManagedReconciliation) ReconcileComputer(ctx context.Context, computerID string) error {
	var err error
	for range reconcileAttempts {
		var inventory machineInventory
		inventory, err = r.machines.inventory(ctx, computerID)
		if errors.Is(err, errMachineUnreachable) {
			return nil
		}
		if err != nil || inventory.Reconciled {
			return err
		}
		var decision machineDecision
		if decision, err = r.decide(ctx, computerID, inventory); err != nil {
			return err
		}
		if err = r.machines.reconcile(ctx, computerID, decision); !errors.Is(err, errRoundSuperseded) {
			return err
		}
	}
	return err
}

// decide names, among what the machine holds, the Works to remove and the
// processes to end. A Work's Computer never changes, so a Work Agent does
// not know, or knows on another Computer, has nothing running here, and is
// removed as a Closed one is.
func (r *ManagedReconciliation) decide(ctx context.Context, computerID string, inventory machineInventory) (machineDecision, error) {
	decision := machineDecision{Round: inventory.Round, RemoveWorks: []string{}, EndProcesses: []machineProcess{}}
	for _, held := range inventory.Works {
		work, err := r.works.sessions.Get(ctx, held.WorkID)
		switch {
		case errors.Is(err, domain.ErrWorkNotFound):
			decision.RemoveWorks = append(decision.RemoveWorks, held.WorkID)
			continue
		case err != nil:
			return machineDecision{}, err
		}
		if work.ComputerID != computerID || work.State == domain.WorkClosed {
			decision.RemoveWorks = append(decision.RemoveWorks, held.WorkID)
			continue
		}
		kept, err := r.runProcesses(ctx, computerID, held.WorkID)
		if err != nil {
			return machineDecision{}, err
		}
		// A stopping or stopped Work's Harness must never run again, which
		// an attach to its exited process would do.
		ending := work.State == domain.WorkStopping || work.State == domain.WorkStopped
		for _, process := range held.Processes {
			if !kept[process] || (ending && slices.Contains(held.Exited, process)) {
				decision.EndProcesses = append(decision.EndProcesses, machineProcess{WorkID: held.WorkID, ProcessID: process})
			}
		}
	}
	return decision, nil
}

// runProcesses are the processes of the run the Work still holds on the
// Computer: its live allocation's, while the Work holds that run.
func (r *ManagedReconciliation) runProcesses(ctx context.Context, computerID, workID string) (map[string]bool, error) {
	var record allocationRecord
	found, err := r.db.Context(ctx).Where("session_id = ? AND deleted_at = 0", workID).Get(&record)
	if err != nil {
		return nil, oops.With("operation", "reconcile_live_allocation").Wrap(err)
	}
	if !found || record.Realization != realizationManaged {
		return map[string]bool{}, nil
	}
	if spec, err := decodeManagedSpecification(record); err != nil || spec.ComputerID != computerID {
		return map[string]bool{}, err
	}
	switch _, err := r.works.session(ctx, record); {
	case errors.Is(err, errAllocationNotHeld), errors.Is(err, errEnvironmentConflict):
		return map[string]bool{}, nil
	case err != nil:
		return nil, err
	}
	kept := map[string]bool{}
	for _, id := range []string{record.PreparationProcessID, record.ProcessID} {
		if id != "" {
			kept[id] = true
		}
	}
	return kept, nil
}

// machineInventory is a round as the machine reports it: the Works it holds
// anything of, with their processes and those of them that exited, while the
// round is not reconciled.
type machineInventory struct {
	Round      string `json:"round"`
	Reconciled bool   `json:"reconciled"`
	Works      []struct {
		WorkID    string   `json:"work_id"`
		Processes []string `json:"processes"`
		Exited    []string `json:"exited"`
	} `json:"works"`
}

// machineDecision is what the machine applies to the round it names.
type machineDecision struct {
	Round        string           `json:"round"`
	RemoveWorks  []string         `json:"remove_works"`
	EndProcesses []machineProcess `json:"end_processes"`
}

type machineProcess struct {
	WorkID    string `json:"work_id"`
	ProcessID string `json:"process_id"`
}

// errRoundSuperseded means the machine reconnected after the round the
// decision was made for.
var errRoundSuperseded = errors.New("agent: the Managed Computer reconnected since the reconciliation round began")

// reconciliationLimit bounds a machine's inventory answer.
const reconciliationLimit = 4 << 20

func (m *ManagedComputers) reconciliationEndpoint(computerID string) string {
	return m.relay.JoinPath("relay", computerID, "reconciliation").String()
}

// inventory reads the machine's current reconciliation round.
func (m *ManagedComputers) inventory(ctx context.Context, computerID string) (machineInventory, error) {
	var inventory machineInventory
	status, body, err := m.callMachine(ctx, http.MethodGet, computerID, nil)
	switch {
	case err != nil:
		return inventory, err
	case status != http.StatusOK:
		// A machine without reconciliation answers 404: it is below the
		// Minimum Version, and nothing proves its leftovers ended.
		return inventory, oops.With("operation", "read_reconciliation", "status", status).Errorf("agent: relay returned HTTP %d", status)
	}
	if err := json.Unmarshal(body, &inventory); err != nil || inventory.Round == "" {
		return inventory, oops.With("operation", "read_reconciliation").Errorf("agent: the Managed Computer answered an unreadable reconciliation round")
	}
	return inventory, nil
}

// reconcile has the machine apply a decision to its round.
func (m *ManagedComputers) reconcile(ctx context.Context, computerID string, decision machineDecision) error {
	document, err := json.Marshal(decision)
	if err != nil {
		return oops.Wrap(err)
	}
	status, _, err := m.callMachine(ctx, http.MethodPost, computerID, document)
	switch {
	case err != nil:
		return err
	case status == http.StatusConflict:
		return errRoundSuperseded
	case status != http.StatusNoContent:
		return oops.With("operation", "apply_reconciliation", "status", status).Errorf("agent: relay returned HTTP %d", status)
	}
	return nil
}

// callMachine makes one control call to the machine itself, naming no Work.
func (m *ManagedComputers) callMachine(ctx context.Context, method, computerID string, document []byte) (int, []byte, error) {
	var body io.Reader
	if document != nil {
		body = bytes.NewReader(document)
	}
	request, err := http.NewRequestWithContext(ctx, method, m.reconciliationEndpoint(computerID), body)
	if err != nil {
		return 0, nil, oops.Wrap(err)
	}
	request.Header.Set("X-Talgent-Control-Token", m.token)
	if document != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := m.http.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return 0, nil, ctx.Err()
		}
		return 0, nil, oops.With("operation", "reconcile_computer").Wrap(err)
	}
	defer response.Body.Close()
	answer, err := io.ReadAll(io.LimitReader(response.Body, reconciliationLimit+1))
	if err != nil {
		return 0, nil, oops.With("operation", "reconcile_computer").Wrap(err)
	}
	if len(answer) > reconciliationLimit {
		return 0, nil, fmt.Errorf("agent: the Managed Computer's reconciliation answer exceeds %d bytes", reconciliationLimit)
	}
	if response.StatusCode == http.StatusServiceUnavailable {
		return 0, nil, errMachineUnreachable
	}
	return response.StatusCode, answer, nil
}
