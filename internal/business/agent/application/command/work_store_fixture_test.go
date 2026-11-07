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
	// committed are the Domain Events of every successful save, in order.
	committed []domain.Event
}

var _ command.AgentWorkRepository = (*feedbackSessionStore)(nil)

func (s *feedbackSessionStore) Get(context.Context, string) (*domain.AgentWork, error) {
	copy := *s.session
	if copy.HarnessProcess != nil {
		process := *copy.HarnessProcess
		copy.HarnessProcess = &process
	}
	// Each load records its own Domain Events, as a repository's does, so a
	// failed save leaves none behind for the next load.
	copy.Events = domain.NewEventCollection()
	return &copy, nil
}
func (s *feedbackSessionStore) Save(_ context.Context, session *domain.AgentWork) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	s.session = session
	s.saves++
	if session.Events != nil {
		s.committed = append(s.committed, session.Events.Drain()...)
	}
	return nil
}

// admittedStarts are the starts the committed Work admitted, each the intent
// its execution begins from.
func (s *feedbackSessionStore) admittedStarts() []domain.WorkStartAdmitted {
	var starts []domain.WorkStartAdmitted
	for _, event := range s.committed {
		if start, ok := event.(domain.WorkStartAdmitted); ok {
			starts = append(starts, start)
		}
	}
	return starts
}

// committedOf counts the committed Domain Events of a kind.
func (s *feedbackSessionStore) committedOf(kind domain.EventKind) int {
	count := 0
	for _, event := range s.committed {
		if event.Kind() == kind {
			count++
		}
	}
	return count
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

// committedStops are the committed requests that the Work's process exit.
func (s *feedbackSessionStore) committedStops() []domain.WorkStopRequested {
	var stops []domain.WorkStopRequested
	for _, event := range s.committed {
		if stop, ok := event.(domain.WorkStopRequested); ok {
			stops = append(stops, stop)
		}
	}
	return stops
}

// stopRequests are the stop identities the committed Work asked its process
// to exit under.
func (s *feedbackSessionStore) stopRequests() []string {
	var stops []string
	for _, event := range s.committed {
		if stop, ok := event.(domain.WorkStopRequested); ok {
			stops = append(stops, stop.StopRequestID)
		}
	}
	return stops
}
