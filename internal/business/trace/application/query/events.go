package query

import (
	"context"
	"time"
)

// NativeEvent is a retained OTLP log record. Attribute namespaces remain distinct.
type NativeEvent struct {
	ID, TraceID, SpanID, Name, Body string
	ProjectID, AgentWorkID          string
	Timestamp                       time.Time
	Attributes, ResourceAttributes  map[string]string
}

type EventOptions struct {
	ProjectID, AgentWorkID, TraceID, CallID, AfterCursor string
	StartTime, EndTime                                   time.Time
	Limit                                                int
}
type EventResult struct {
	Events     []NativeEvent
	NextCursor string
}

// ListEvents reads a Project's events, or, without a Project, one Work's.
func (q *Queries) ListEvents(ctx context.Context, opts EventOptions) (*EventResult, error) {
	if err := validateTimeRange(opts.StartTime, opts.EndTime); err != nil {
		return nil, err
	}
	scope, err := q.authorizeScope(ctx, WorkScope{ProjectID: opts.ProjectID, AgentWorkID: opts.AgentWorkID}, false)
	if err != nil {
		return nil, err
	}
	opts.ProjectID, opts.AgentWorkID = scope.ProjectID, scope.AgentWorkID
	if opts.Limit <= 0 || opts.Limit > 500 {
		opts.Limit = 500
	}
	return q.evidence.ListEvents(ctx, opts)
}
