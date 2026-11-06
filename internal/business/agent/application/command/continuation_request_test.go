package command_test

import (
	"context"
	"sync"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
)

type continuationRequests struct {
	mu    sync.Mutex
	items map[string]command.ContinuationRequest
}

func (r *continuationRequests) RetainContinuation(_ context.Context, input command.ContinuationRequest, _ int) (command.ContinuationRequest, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.items == nil {
		r.items = make(map[string]command.ContinuationRequest)
	}
	old, found := r.items[input.WorkID]
	if found && (old.RequestID == input.RequestID || !old.Resolved) {
		if old.SubjectID != input.SubjectID || old.Operation != input.Operation {
			return command.ContinuationRequest{}, command.ErrContinuationConflict
		}
		return old, nil
	}
	r.items[input.WorkID] = input
	return input, nil
}
func (r *continuationRequests) GetContinuation(_ context.Context, id string) (command.ContinuationRequest, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.items[id]
	return v, ok, nil
}
func (r *continuationRequests) RecordContinuationRejection(_ context.Context, id, requestID, reason string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	v := r.items[id]
	if v.RequestID != requestID {
		return command.ErrContinuationConflict
	}
	v.Rejection = reason
	r.items[id] = v
	return nil
}
func (r *continuationRequests) ResolveContinuation(_ context.Context, id, requestID string, accepted bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	v := r.items[id]
	if v.RequestID != requestID {
		return command.ErrContinuationConflict
	}
	v.Resolved = true
	if accepted {
		v.Rejection = ""
	}
	r.items[id] = v
	return nil
}
