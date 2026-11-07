package command_test

import (
	"context"
	"sync"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// feedbackSessionStore holds one Work and its start-request records, standing
// in for the AgentWork repository and its request records.
type feedbackSessionStore struct {
	command.AgentWorkRepository
	mu       sync.Mutex
	session  *domain.AgentWork
	saves    int
	saveErr  error
	requests map[string]query.StartRequest
	// commitErr fails the next commit of a start request once, after its
	// Work and record were not written.
	commitErr error
}

var _ command.AgentWorkRepository = (*feedbackSessionStore)(nil)

func (s *feedbackSessionStore) Get(context.Context, string) (*domain.AgentWork, error) {
	copy := *s.session
	if copy.HarnessProcess != nil {
		process := *copy.HarnessProcess
		copy.HarnessProcess = &process
	}
	return &copy, nil
}
func (s *feedbackSessionStore) Save(_ context.Context, session *domain.AgentWork) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	s.session = session
	s.saves++
	return nil
}

func (s *feedbackSessionStore) FindStartRequest(_ context.Context, requestID string) (query.StartRequest, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, found := s.requests[requestID]
	return record, found, nil
}

// record retains a request unless its identity is recorded, as the first
// writer decides it, and answers the retained record.
func (s *feedbackSessionStore) record(request query.StartRequest) query.StartRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.requests == nil {
		s.requests = map[string]query.StartRequest{}
	}
	if retained, found := s.requests[request.ID]; found {
		return retained
	}
	s.requests[request.ID] = request
	return request
}

func (s *feedbackSessionStore) forget(requestID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.requests, requestID)
}

func (s *feedbackSessionStore) CommitStart(ctx context.Context, work *domain.AgentWork, request query.StartRequest) error {
	if err := s.failCommit(); err != nil {
		return err
	}
	if err := command.AnswerRetained(s.record(request), request); err != nil {
		return err
	}
	if err := s.Save(ctx, work); err != nil {
		s.forget(request.ID)
		return err
	}
	return nil
}

func (s *feedbackSessionStore) failCommit() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.commitErr
	s.commitErr = nil
	return err
}

func (s *feedbackSessionStore) CancelStart(_ context.Context, workID string, requestID string) (bool, error) {
	retained := s.record(query.StartRequest{ID: requestID, WorkID: workID, Canceled: true})
	return retained.Canceled, nil
}

func (s *feedbackSessionStore) RecordCreation(_ context.Context, request query.StartRequest) (query.StartRequest, error) {
	return s.record(request), nil
}

// observedProcess supplies a fixed current observation of the Work's process.
// observedProcess is the environment authority for the Work's current process,
// "process" unless a test names another.
type observedProcess struct {
	observation  domain.ProcessObservation
	calls        int
	current      string
	acknowledged []string
	// attachment is Dispatcher's attachment to the process's sandbox;
	// observeErr fails the observation instead.
	attachment command.SandboxAttachment
	observeErr error
}

func (o *observedProcess) ObserveProcess(context.Context, string) (command.WorkProcessObservation, error) {
	o.calls++
	if o.observeErr != nil {
		return command.WorkProcessObservation{}, o.observeErr
	}
	return command.WorkProcessObservation{ProcessObservation: o.observation, Attachment: o.attachment}, nil
}

func (o *observedProcess) currentProcess() string {
	if o.current == "" {
		return "process"
	}
	return o.current
}

func (o *observedProcess) IsCurrentProcess(_ context.Context, _ string, processID string) (bool, error) {
	return processID == o.currentProcess(), nil
}

func (o *observedProcess) AcknowledgeExit(_ context.Context, _ string, processID string) (bool, error) {
	if processID != o.currentProcess() {
		return false, nil
	}
	o.acknowledged = append(o.acknowledged, processID)
	return true, nil
}

func exitedProcess() *observedProcess {
	return &observedProcess{observation: domain.ProcessObservation{State: domain.ProcessPending}}
}
