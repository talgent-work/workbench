package command_test

import (
	"context"
	"sync"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// feedbackSessionStore holds one Work and its start-request records, standing
// in for the AgentWork repository and its start admissions.
type feedbackSessionStore struct {
	command.AgentWorkRepository
	mu       sync.Mutex
	session  *domain.AgentWork
	saves    int
	saveErr  error
	starts   map[string]query.AdmittedStart
	canceled map[string]bool
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

func (s *feedbackSessionStore) FindAdmittedStart(_ context.Context, requestID string) (query.AdmittedStart, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	start, found := s.starts[requestID]
	return start, found, nil
}

func (s *feedbackSessionStore) CommitStart(ctx context.Context, work *domain.AgentWork, start query.AdmittedStart, requestID string) error {
	s.mu.Lock()
	canceled := s.canceled[requestID]
	s.mu.Unlock()
	if canceled {
		return command.ErrStartRequestCanceled
	}
	if err := s.Save(ctx, work); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.starts == nil {
		s.starts = map[string]query.AdmittedStart{}
	}
	s.starts[requestID] = start
	return nil
}

func (s *feedbackSessionStore) CancelStart(_ context.Context, _ string, requestID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, admitted := s.starts[requestID]; admitted {
		return false, nil
	}
	if s.canceled == nil {
		s.canceled = map[string]bool{}
	}
	s.canceled[requestID] = true
	return true, nil
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
