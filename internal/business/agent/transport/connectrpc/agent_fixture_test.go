package connectrpc_test

import (
	"context"

	domainevent "github.com/go-jimu/components/ddd/event"

	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

type rpcAgents struct {
	items map[string]*domain.AgentDefinition
}

func (r *rpcAgents) Get(_ context.Context, id string) (*domain.AgentDefinition, error) {
	a, ok := r.items[id]
	if !ok {
		return nil, domain.ErrAgentDefinitionNotFound
	}
	clone := *a
	return &clone, nil
}
func (r *rpcAgents) Save(_ context.Context, a *domain.AgentDefinition) error {
	clone := *a
	r.items[a.ID] = &clone
	return nil
}

type rpcSessions struct {
	domain.AgentWorkRepository
	items map[string]*domain.AgentWork
	// events receives the Work's Domain Events once a change is saved, as the
	// production repository dispatches them.
	events domainevent.Dispatcher
}

func (r *rpcSessions) Get(_ context.Context, id string) (*domain.AgentWork, error) {
	s, ok := r.items[id]
	if !ok {
		return nil, domain.ErrWorkNotFound
	}
	clone := *s
	if s.HarnessProcess != nil {
		process := *s.HarnessProcess
		clone.HarnessProcess = &process
	}
	// Each load is a fresh reconstitution with its own event collection.
	clone.Events = domain.NewEventCollection()
	return &clone, nil
}
func (r *rpcSessions) Save(ctx context.Context, s *domain.AgentWork) error {
	clone := *s
	clone.Version++
	r.items[s.ID] = &clone
	if r.events != nil && s.Events != nil && s.Events.Len() > 0 {
		return r.events.DispatchAll(ctx, s.Events.Drain())
	}
	return nil
}

type rpcAuthorization struct{}

func (*rpcAuthorization) AuthorizeWork(_ context.Context, subject, id string) error {
	if subject != "owner" {
		return access.ErrAccessDenied
	}
	return nil
}

// rpcExecution records the inputs and replies handed to the Work's buffer.
type rpcExecution struct {
	inputs    []command.ExecutionInput
	responses []command.InteractionResponse
}

func (p *rpcExecution) AcceptInput(_ context.Context, _ string, input command.ExecutionInput) (command.InputHandOff, error) {
	p.inputs = append(p.inputs, input)
	return command.InputBuffered, nil
}

func (p *rpcExecution) AcceptResponse(_ context.Context, _ string, response command.InteractionResponse) error {
	p.responses = append(p.responses, response)
	return nil
}

func (r *rpcSessions) FindByWorkKey(_ context.Context, agentID string, key domain.WorkKey) (*domain.AgentWork, error) {
	for _, session := range r.items {
		if session.AgentID == agentID && session.WorkKey == key {
			return r.Get(context.Background(), session.ID)
		}
	}
	return nil, nil
}

func (*rpcExecution) HandOffInitialTaskInput(context.Context, string) error { return nil }
