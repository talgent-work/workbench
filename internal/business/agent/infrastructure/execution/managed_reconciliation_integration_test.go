//go:build integration

package execution

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// machineRounds plays a Talgent Computer's reconciliation rounds: each new
// connection opens a round, its inventory lists what the machine holds, and
// it applies a decision for the current round only.
type machineRounds struct {
	mu        sync.Mutex
	round     int
	applied   bool
	holds     map[string][]string
	exited    map[string][]string
	decisions []machineDecision
	// reconnectOnApply opens a new round instead of applying, as when the
	// machine reconnected while the decision was made.
	reconnectOnApply int
}

func (m *machineRounds) roundID() string { return "round-" + string(rune('a'+m.round)) }

func (m *machineRounds) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r.Header.Get("X-Talgent-Control-Token") != "computer-control" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	switch r.Method {
	case http.MethodGet:
		inventory := map[string]any{"round": m.roundID(), "reconciled": m.applied, "works": []any{}}
		if !m.applied {
			works := []any{}
			for _, id := range slices.Sorted(func(yield func(string) bool) {
				for id := range m.holds {
					if !yield(id) {
						return
					}
				}
			}) {
				works = append(works, map[string]any{"work_id": id, "processes": m.holds[id], "exited": append([]string{}, m.exited[id]...)})
			}
			inventory["works"] = works
		}
		_ = json.NewEncoder(w).Encode(inventory)
	case http.MethodPost:
		var decision machineDecision
		if json.NewDecoder(r.Body).Decode(&decision) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if m.reconnectOnApply > 0 {
			m.reconnectOnApply--
			m.round++
		}
		if decision.Round != m.roundID() {
			w.WriteHeader(http.StatusConflict)
			return
		}
		m.decisions = append(m.decisions, decision)
		m.applied = true
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// decided reports, as the machine's ping does, whether the current round is
// decided.
func (m *machineRounds) decided() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.applied
}

func (m *machineRounds) reconnect() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.round++
	m.applied = false
}

// reconciledWorks holds Works as Agent records them for the reconciliation.
type reconciledWorks struct {
	mu    sync.Mutex
	works map[string]*domain.AgentWork
}

func (w *reconciledWorks) Get(_ context.Context, id string) (*domain.AgentWork, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	work, ok := w.works[id]
	if !ok {
		return nil, domain.ErrWorkNotFound
	}
	copied := *work
	return &copied, nil
}

func (*reconciledWorks) Save(context.Context, *domain.AgentWork) error { return nil }

// A reconnected machine is reconciled before anything new starts on it: the
// first step of a start that may launch a process finds the round undecided
// in its ping and has the machine end every process outside the runs Agent's
// Works still hold there and remove every Work gone from it (Closed, unknown
// to Agent or of another Computer), naming exactly what the machine
// reported. A stopping Work keeps no exited process. A reconnection while
// the decision was made is followed into the new round, and a decided round
// costs no call beyond the ping.
func TestAReconnectedMachineEndsLeftoversAndRemovesClosedWorksBeforeAnythingStarts(t *testing.T) {
	ctx := context.Background()
	f := newManagedWork(t)
	const (
		running   = "01900000-0000-7000-8000-0000000000a1"
		stopped   = "01900000-0000-7000-8000-0000000000a2"
		closed    = "01900000-0000-7000-8000-0000000000a3"
		unknown   = "01900000-0000-7000-8000-0000000000a4"
		elsewhere = "01900000-0000-7000-8000-0000000000a5"
	)
	works := &reconciledWorks{works: map[string]*domain.AgentWork{
		running:   {ID: running, ComputerID: managedComputerID, State: domain.WorkWorking, HarnessProcess: &domain.HarnessProcess{}},
		stopped:   {ID: stopped, ComputerID: managedComputerID, State: domain.WorkStopped, HarnessProcess: &domain.HarnessProcess{Exited: true, ComputeReclaimed: true}},
		closed:    {ID: closed, ComputerID: managedComputerID, State: domain.WorkClosed, HarnessProcess: &domain.HarnessProcess{Exited: true, ComputeReclaimed: true}},
		elsewhere: {ID: elsewhere, ComputerID: "01900000-0000-7000-8000-0000000000c2", State: domain.WorkClosed},
	}}
	held := NewHeldWorks(f.db, works)
	f.provisioning.credentials.works = held
	f.provisioning.managed.works = held
	f.provisioning.managed.reconciliation.works = held
	startManaged(t, f, running)

	rounds := &machineRounds{holds: map[string][]string{
		running:   {"preparation-handle", "harness-handle", "stale-handle"},
		stopped:   {"old-handle"},
		closed:    {"closed-handle"},
		unknown:   {"unknown-handle"},
		elsewhere: {"elsewhere-handle"},
	}, exited: map[string][]string{running: {"preparation-handle"}}}
	f.relay.set(func(m *machineRelay) { m.reconciliation = rounds; m.calls = nil })
	rounds.reconnect()
	rounds.reconnectOnApply = 1

	// The Work's next start resolves its environment first.
	record, found, err := f.provisioning.live(ctx, running)
	require.NoError(t, err)
	require.True(t, found)
	_, state, err := f.provisioning.managed.resolve(ctx, record, nil)
	require.NoError(t, err)
	require.Equal(t, environmentRunning, state)

	require.Len(t, rounds.decisions, 1, "the decision for the round the machine left is refused and made again")
	decision := rounds.decisions[0]
	require.Equal(t, "round-c", decision.Round)
	require.ElementsMatch(t, []string{closed, unknown, elsewhere}, decision.RemoveWorks, "a Work gone from the machine loses its processes and files")
	require.ElementsMatch(t, []machineProcess{{WorkID: running, ProcessID: "stale-handle"}, {WorkID: stopped, ProcessID: "old-handle"}}, decision.EndProcesses,
		"the run a Working Work holds keeps its processes, exited ones included; every other process ends")
	require.Equal(t, []string{
		http.MethodGet + " /relay/" + managedComputerID + "/works/" + running + "/ping",
		http.MethodGet + " /relay/" + managedComputerID + "/reconciliation",
		http.MethodPost + " /relay/" + managedComputerID + "/reconciliation",
		http.MethodGet + " /relay/" + managedComputerID + "/reconciliation",
		http.MethodPost + " /relay/" + managedComputerID + "/reconciliation",
	}, f.relay.calls)

	// A decided round, as the ping reports it, costs no further call.
	f.relay.set(func(m *machineRelay) { m.calls = nil })
	_, _, err = f.provisioning.managed.resolve(ctx, record, nil)
	require.NoError(t, err)
	require.Len(t, rounds.decisions, 1)
	require.Equal(t, []string{http.MethodGet + " /relay/" + managedComputerID + "/works/" + running + "/ping"}, f.relay.calls)

	// A stopping Work still holds its run, yet keeps no exited process, which
	// an attach would run again.
	works.mu.Lock()
	works.works[running] = &domain.AgentWork{ID: running, ComputerID: managedComputerID, State: domain.WorkStopping, HarnessProcess: &domain.HarnessProcess{}}
	works.mu.Unlock()
	rounds.reconnect()
	rounds.exited[running] = []string{"harness-handle"}
	require.NoError(t, f.provisioning.managed.Reconciliation().ReconcileComputer(ctx, managedComputerID))
	require.Len(t, rounds.decisions, 2)
	require.ElementsMatch(t, []machineProcess{
		{WorkID: running, ProcessID: "harness-handle"}, {WorkID: running, ProcessID: "stale-handle"},
		{WorkID: stopped, ProcessID: "old-handle"},
	}, rounds.decisions[1].EndProcesses)

	// Once the Work stopped beyond reach its run is no longer held: the next
	// reconnection ends its processes too.
	works.mu.Lock()
	works.works[running] = &domain.AgentWork{ID: running, ComputerID: managedComputerID, State: domain.WorkStopped, HarnessProcess: &domain.HarnessProcess{Exited: true, ComputeReclaimed: true}}
	works.mu.Unlock()
	rounds.reconnect()
	require.NoError(t, f.provisioning.managed.Reconciliation().ReconcileComputer(ctx, managedComputerID))
	require.Len(t, rounds.decisions, 3)
	require.ElementsMatch(t, []machineProcess{
		{WorkID: running, ProcessID: "preparation-handle"}, {WorkID: running, ProcessID: "harness-handle"}, {WorkID: running, ProcessID: "stale-handle"},
		{WorkID: stopped, ProcessID: "old-handle"},
	}, rounds.decisions[2].EndProcesses)

	// A machine the relay does not reach has nothing to reconcile until it
	// connects. One without reconciliation is below the Minimum Version:
	// nothing proves its leftovers ended, so its steps fail.
	f.relay.set(func(m *machineRelay) { m.unreachable = true })
	require.NoError(t, f.provisioning.managed.Reconciliation().ReconcileComputer(ctx, managedComputerID))
	f.relay.set(func(m *machineRelay) { m.unreachable, m.reconciliation, m.undecided = false, nil, true })
	require.Error(t, f.provisioning.managed.Reconciliation().ReconcileComputer(ctx, managedComputerID))
	_, state, err = f.provisioning.managed.resolve(ctx, record, nil)
	require.Error(t, err)
	require.Equal(t, environmentPending, state)
}
