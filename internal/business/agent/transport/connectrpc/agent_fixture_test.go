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
	requests map[string]query.StartRequest
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

type rpcExecution struct {
	inputs    []command.ExecutionInput
	responses []command.InteractionResponse
	starts    []domain.StartExecutionRequest
	stops     []string
	reclaims  []string
}

func (p *rpcExecution) StartExecution(_ context.Context, r domain.StartExecutionRequest) error {
	p.starts = append(p.starts, r)
	return nil
}
func (p *rpcExecution) StopExecution(_ context.Context, _ string, id string) error {
	p.stops = append(p.stops, id)
	return nil
}
func (p *rpcExecution) AcceptInput(_ context.Context, _ string, input command.ExecutionInput) (command.InputHandOff, error) {
	p.inputs = append(p.inputs, input)
	return command.InputBuffered, nil
}
func (p *rpcExecution) ReclaimCompute(_ context.Context, r string) error {
	p.reclaims = append(p.reclaims, r)
	return nil
}

func (*rpcExecution) RetireFencedRun(context.Context, string) error { return nil }

func (r *rpcSessions) RegisterWork(ctx context.Context, s *domain.AgentWork, registration command.WorkRegistration) error {
	if retained := r.record(registration.Request); retained != registration.Request {
		if retained.Canceled {
			return command.ErrWorkCreationCanceled
		}
		return command.ErrStartRequestConflict
	}
	return r.Save(ctx, s)
}

func (p *rpcExecution) AcceptResponse(_ context.Context, _ string, response command.InteractionResponse) error {
	p.responses = append(p.responses, response)
	return nil
}

func (r *rpcSessions) FindStartRequest(_ context.Context, requestID string) (query.StartRequest, bool, error) {
	record, found := r.requests[requestID]
	return record, found, nil
}

// record retains a request unless its identity is recorded, as the first
// writer decides it, and answers the retained record.
func (r *rpcSessions) record(request query.StartRequest) query.StartRequest {
	if retained, found := r.requests[request.ID]; found {
		return retained
	}
	if r.requests == nil {
		r.requests = map[string]query.StartRequest{}
	}
	r.requests[request.ID] = request
	return request
}

func (r *rpcSessions) CommitStart(ctx context.Context, work *domain.AgentWork, request query.StartRequest) error {
	if err := command.AnswerRetained(r.record(request), request); err != nil {
		return err
	}
	return r.Save(ctx, work)
}

func (r *rpcSessions) CancelStart(_ context.Context, workID string, requestID string) (bool, error) {
	return r.record(query.StartRequest{ID: requestID, WorkID: workID, Canceled: true}).Canceled, nil
}

func (r *rpcSessions) RecordCreation(_ context.Context, request query.StartRequest) (query.StartRequest, error) {
	return r.record(request), nil
}

// admitted are the start requests committed with a start.
func (r *rpcSessions) admitted() []query.StartRequest {
	var admitted []query.StartRequest
	for _, record := range r.requests {
		if record.Admitted() {
			admitted = append(admitted, record)
		}
	}
	return admitted
}

type rpcProcesses struct{ observation domain.ProcessObservation }

func (p *rpcProcesses) ObserveProcess(context.Context, string) (command.WorkProcessObservation, error) {
	return command.WorkProcessObservation{ProcessObservation: p.observation}, nil
}

// The Work's current process is "process" in these fixtures.
func (*rpcProcesses) IsCurrentProcess(_ context.Context, _ string, processID string) (bool, error) {
	return processID == "process", nil
}

func (*rpcProcesses) AcknowledgeExit(_ context.Context, _ string, processID string) (bool, error) {
	return processID == "process", nil
}

func (r *rpcSessions) LocateWork(_ context.Context, workID string) (*domain.AgentWork, error) {
	for _, session := range r.items {
		if session.ID == workID {
			return session, nil
		}
	}
	return nil, domain.ErrWorkNotFound
}

func (r *rpcSessions) FindWork(_ context.Context, agentID string, subject domain.WorkSubject) (*domain.AgentWork, error) {
	for _, session := range r.items {
		if session.AgentID == agentID && session.Subject == subject {
			return session, nil
		}
	}
	return nil, nil
}

func (*rpcExecution) HandOffInitialTaskInput(context.Context, string) error { return nil }

func (*rpcExecution) CancelRunningTurn(context.Context, string) error { return nil }
