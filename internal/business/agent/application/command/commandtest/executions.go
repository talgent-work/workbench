package commandtest

import (
	"context"
	"slices"
	"sync"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// Executions stands in for the AgentExecution repository: it keeps a copy of
// each saved execution under an optimistic version, answers a Work's most
// recently begun one, refuses a second unretired one per Work, and keeps the
// Domain Events each save committed. A test may fail the next saves with a
// concurrent modification or another error.
type Executions struct {
	mu    sync.Mutex
	saved map[string]domain.AgentExecution
	order []string
	// Committed are the Domain Events of every successful save, in order.
	Committed []domain.Event
	// Saves counts successful saves.
	Saves int
	// Conflicts fails that many next saves with a concurrent modification.
	Conflicts int
	// SaveErr fails every save while set.
	SaveErr error
}

var _ domain.AgentExecutionRepository = (*Executions)(nil)

func (r *Executions) Get(_ context.Context, id string) (*domain.AgentExecution, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	saved, found := r.saved[id]
	if !found {
		return nil, domain.ErrExecutionNotFound
	}
	return loaded(saved), nil
}

func (r *Executions) Latest(_ context.Context, workID string) (*domain.AgentExecution, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, id := range slices.Backward(r.order) {
		if saved := r.saved[id]; saved.WorkID == workID {
			return loaded(saved), nil
		}
	}
	return nil, domain.ErrExecutionNotFound
}

func (r *Executions) Save(_ context.Context, execution *domain.AgentExecution) error {
	if err := execution.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.SaveErr != nil {
		return r.SaveErr
	}
	if r.Conflicts > 0 {
		r.Conflicts--
		return domain.ErrConcurrentModification
	}
	if r.saved == nil {
		r.saved = map[string]domain.AgentExecution{}
	}
	stored := *execution
	if execution.Version == 0 {
		for _, other := range r.saved {
			if other.WorkID == execution.WorkID && !other.Retired() {
				return domain.ErrExecutionAdmission
			}
		}
		r.order = append(r.order, execution.ID)
	} else if current, found := r.saved[execution.ID]; !found || current.Version != execution.Version {
		return domain.ErrConcurrentModification
	}
	stored.Version = execution.Version + 1
	stored.Events = nil
	r.saved[execution.ID] = stored
	if execution.Events != nil {
		r.Committed = append(r.Committed, execution.Events.Drain()...)
	}
	r.Saves++
	return nil
}

// Put stores an execution as it is, as a test's starting point.
func (r *Executions) Put(execution *domain.AgentExecution) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.saved == nil {
		r.saved = map[string]domain.AgentExecution{}
	}
	if _, found := r.saved[execution.ID]; !found {
		r.order = append(r.order, execution.ID)
	}
	stored := *execution
	if stored.Version == 0 {
		stored.Version = 1
	}
	stored.Events = nil
	r.saved[execution.ID] = stored
}

// Count is how many executions were begun.
func (r *Executions) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.order)
}

// CommittedKinds lists the kinds of the committed Domain Events.
func (r *Executions) CommittedKinds() []domain.EventKind {
	r.mu.Lock()
	defer r.mu.Unlock()
	kinds := make([]domain.EventKind, 0, len(r.Committed))
	for _, event := range r.Committed {
		kinds = append(kinds, event.Kind())
	}
	return kinds
}

func loaded(saved domain.AgentExecution) *domain.AgentExecution {
	copy := saved
	copy.Events = domain.NewEventCollection()
	return &copy
}
