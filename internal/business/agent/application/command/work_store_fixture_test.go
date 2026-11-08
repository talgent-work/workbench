package command_test

import (
	"context"
	"sync"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// feedbackSessionStore holds one Work, standing in for the AgentWork
// repository.
type feedbackSessionStore struct {
	domain.AgentWorkRepository
	mu      sync.Mutex
	session *domain.AgentWork
	saves   int
	saveErr error
	// commitErr fails the next save once, without writing the Work;
	// answerErr fails it once after writing the Work, an answer lost after
	// the commit landed.
	commitErr, answerErr error
	// committed are the Domain Events of every successful save, in order.
	committed []domain.Event
}

var _ domain.AgentWorkRepository = (*feedbackSessionStore)(nil)

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
	if err := s.failCommit(); err != nil {
		return err
	}
	if s.saveErr != nil {
		return s.saveErr
	}
	s.session = session
	s.saves++
	if session.Events != nil {
		s.committed = append(s.committed, session.Events.Drain()...)
	}
	return s.loseAnswer()
}

// admittedStarts are the starts the committed Work admitted, each the request
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

func (s *feedbackSessionStore) failCommit() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.commitErr
	s.commitErr = nil
	return err
}

func (s *feedbackSessionStore) loseAnswer() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.answerErr
	s.answerErr = nil
	return err
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
