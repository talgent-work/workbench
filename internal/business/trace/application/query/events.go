package query

import (
	"context"
	"errors"
	"time"
)

// NativeEvent is a retained OTLP log record. Attribute namespaces remain distinct.
type NativeEvent struct {
	ID, TraceID, SpanID, Name, Body string
	WorkCaller                      WorkCaller
	WorkKey                         WorkKey
	AgentWorkID                     string
	Timestamp                       time.Time
	Attributes, ResourceAttributes  map[string]string
}

// EventOptions names a Work Caller's events, narrowed to one Work when
// AgentWorkID is set.
type EventOptions struct {
	WorkCaller                                WorkCaller
	AgentWorkID, TraceID, CallID, AfterCursor string
	StartTime, EndTime                        time.Time
	Limit                                     int
}
type EventResult struct {
	Events     []NativeEvent
	NextCursor string
}

// ListEvents reads an Extension Work Caller's events, narrowed to one of its
// Works when named, or one Standalone Work's.
func (q *Queries) ListEvents(ctx context.Context, opts EventOptions) (*EventResult, error) {
	if err := validateTimeRange(opts.StartTime, opts.EndTime); err != nil {
		return nil, err
	}
	scope, err := q.authorizeScope(ctx, WorkScope{WorkCaller: opts.WorkCaller, AgentWorkID: opts.AgentWorkID}, false)
	if errors.Is(err, errWorkNotObserved) {
		return &EventResult{}, nil
	}
	if err != nil {
		return nil, err
	}
	opts.WorkCaller, opts.AgentWorkID = scope.WorkCaller, scope.AgentWorkID
	if opts.Limit <= 0 || opts.Limit > 500 {
		opts.Limit = 500
	}
	return q.evidence.ListEvents(ctx, opts)
}
