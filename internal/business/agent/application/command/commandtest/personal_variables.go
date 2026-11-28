package commandtest

import (
	"context"
	"slices"
	"sync"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// PersonalVariables holds PersonalVariableSets in memory; its zero value
// holds none.
type PersonalVariables struct {
	mu   sync.Mutex
	sets map[string]domain.PersonalVariableSet
}

var _ domain.PersonalVariableSetRepository = (*PersonalVariables)(nil)

func (p *PersonalVariables) Get(_ context.Context, personID string) (*domain.PersonalVariableSet, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	set, found := p.sets[personID]
	if !found {
		return domain.NewPersonalVariableSet(personID)
	}
	set.Variables = slices.Clone(set.Variables)
	return &set, nil
}

func (p *PersonalVariables) Save(_ context.Context, set *domain.PersonalVariableSet) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if current := p.sets[set.PersonID]; current.Version != set.Version {
		return domain.ErrConcurrentModification
	}
	if p.sets == nil {
		p.sets = map[string]domain.PersonalVariableSet{}
	}
	saved := *set
	saved.Variables, saved.Version = slices.Clone(set.Variables), set.Version+1
	p.sets[set.PersonID] = saved
	return nil
}
