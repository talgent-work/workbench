package connectrpc_test

import (
	"context"

	domainevent "github.com/go-jimu/components/ddd/event"

	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
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
	command.AgentWorkRepository
	items    map[string]*domain.AgentWork
	starts   map[string]query.AdmittedStart
	canceled map[string]bool
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

func (r *rpcSessions) RegisterWork(ctx context.Context, s *domain.AgentWork, registration command.WorkRegistration) error {
	return r.CommitStart(ctx, s, query.AdmittedStart{WorkID: s.ID, Kind: query.StartKindStart}, registration.RequestID)
}

func (p *rpcExecution) AcceptResponse(_ context.Context, _ string, response command.InteractionResponse) error {
	p.responses = append(p.responses, response)
	return nil
}

func (r *rpcSessions) FindAdmittedStart(_ context.Context, requestID string) (query.AdmittedStart, bool, error) {
	start, found := r.starts[requestID]
	return start, found, nil
}

func (r *rpcSessions) CommitStart(ctx context.Context, work *domain.AgentWork, start query.AdmittedStart, requestID string) error {
	if r.canceled[requestID] {
		return command.ErrStartRequestCanceled
	}
	if err := r.Save(ctx, work); err != nil {
		return err
	}
	if r.starts == nil {
		r.starts = map[string]query.AdmittedStart{}
	}
	r.starts[requestID] = start
	return nil
}

func (r *rpcSessions) CancelStart(_ context.Context, _ string, requestID string) (bool, error) {
	if _, admitted := r.starts[requestID]; admitted {
		return false, nil
	}
	if r.canceled == nil {
		r.canceled = map[string]bool{}
	}
	r.canceled[requestID] = true
	return true, nil
}

func (r *rpcSessions) FindBySubject(_ context.Context, agentID string, subject domain.WorkSubject) (*domain.AgentWork, error) {
	for _, session := range r.items {
		if session.AgentID == agentID && session.Subject == subject {
			return r.Get(context.Background(), session.ID)
		}
	}
	return nil, nil
}

type rpcInitialInputs struct{}

func (rpcInitialInputs) Get(context.Context, string) (command.InitialExecutionRequest, error) {
	return command.InitialExecutionRequest{}, command.ErrInitialExecutionNotFound
}
