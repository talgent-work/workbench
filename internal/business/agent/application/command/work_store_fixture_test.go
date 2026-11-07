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
