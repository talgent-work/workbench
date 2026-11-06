package execution

import (
	"context"
	"errors"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"xorm.io/xorm"
)

// HeldWorks says whether a live allocation still backs its Work's Harness
// process, which every credential delivery, on any realization, is checked
// against: a delivery is made only while the Work starts or works, and a Work
// whose stop is admitted is drained.
type HeldWorks struct {
	db       *xorm.Engine
	sessions domain.AgentWorkRepository
}

func NewHeldWorks(db *xorm.Engine, sessions domain.AgentWorkRepository) *HeldWorks {
	return &HeldWorks{db: db, sessions: sessions}
}

func (h *HeldWorks) session(ctx context.Context, record allocationRecord) (*domain.AgentWork, error) {
	var actual allocationRecord
	found, err := h.db.Context(ctx).Where("id = ?", record.ID).Get(&actual)
	if err != nil {
		return nil, errors.New("agent: credential allocation unavailable")
	}
	if !found || actual.DeletedAt != 0 || actual.SandboxID != record.SandboxID || actual.SessionID != record.SessionID {
		return nil, errEnvironmentConflict
	}
	session, err := h.sessions.Get(ctx, record.SessionID)
	if err != nil {
		return nil, errors.New("agent: credential session unavailable")
	}
	// A live allocation is the Work's only one; it backs the Harness process
	// only until that process's exit is confirmed.
	if !session.HoldsProcess() {
		return nil, errAllocationNotHeld
	}
	return session, nil
}

func (h *HeldWorks) current(ctx context.Context, record allocationRecord) error {
	session, err := h.session(ctx, record)
	if err != nil {
		return err
	}
	if session.State != domain.WorkStarting && session.State != domain.WorkWorking {
		return errAllocationNotHeld
	}
	return nil
}

// A closing current process may still need its native connection to deliver
// the already-admitted Stop and observe exit. It receives an empty Vault, never
// refreshed material. This preserves AgentWork's existing recovery contract.
func (h *HeldWorks) draining(ctx context.Context, record allocationRecord) (bool, error) {
	session, err := h.session(ctx, record)
	if err != nil {
		return false, err
	}
	if session.State == domain.WorkStarting || session.State == domain.WorkWorking {
		return false, nil
	}
	if session.StopRequestReference != "" && (session.State == domain.WorkStopping || session.State == domain.WorkClosing || session.State == domain.WorkFailed) {
		return true, nil
	}
	return false, errAllocationNotHeld
}

// unreached reports that the allocation backs its Work's current run and the
// Work reclaims that run beyond reach. An allocation its Work no longer
// holds, or another one replaced, is not reclaimed for it.
func (h *HeldWorks) unreached(ctx context.Context, record allocationRecord) (bool, error) {
	session, err := h.session(ctx, record)
	switch {
	case errors.Is(err, errAllocationNotHeld), errors.Is(err, errEnvironmentConflict):
		return false, nil
	case err != nil:
		return false, err
	}
	return session.HarnessProcess.Unreached, nil
}

// outlived reports that the allocation's Work no longer holds the run it
// backs: the Work recorded its end. An allocation another one replaced is
// not the Work's to judge.
func (h *HeldWorks) outlived(ctx context.Context, record allocationRecord) (bool, error) {
	_, err := h.session(ctx, record)
	switch {
	case errors.Is(err, errAllocationNotHeld):
		return true, nil
	case errors.Is(err, errEnvironmentConflict):
		return false, nil
	}
	return false, err
}
