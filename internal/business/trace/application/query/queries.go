package query

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Queries owns authorized native evidence and derived measurement reads.
type Queries struct {
	evidence     NativeEvidenceQueryRepository
	measurements MeasurementQueryRepository
	workCallers  WorkCallerAccessPort
	agent        AgentPort
	accounts     AccountPort
}

func NewQueries(evidence NativeEvidenceQueryRepository, measurements MeasurementQueryRepository, workCallers WorkCallerAccessPort, agent AgentPort, accounts AccountPort) *Queries {
	return &Queries{evidence: evidence, measurements: measurements, workCallers: workCallers, agent: agent, accounts: accounts}
}

var (
	ErrInvalidQuery = errors.New("invalid trace query")
	ErrSpanNotFound = errors.New("span not found")
	// ErrWorkNotFound is a Work named in a scope it is not in: under a Work
	// Caller other than its own, or an Extension Work named as a Standalone
	// one.
	ErrWorkNotFound = errors.New("work not found in the named scope")
)

func required(value, name string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%w: %s is required", ErrInvalidQuery, name)
	}
	return value, nil
}

func validateTimeRange(start, end time.Time) error {
	if !start.IsZero() && !end.IsZero() && !start.Before(end) {
		return fmt.Errorf("%w: start_time must precede end_time", ErrInvalidQuery)
	}
	return nil
}

func validateMetricOptions(opts TraceMetricOptions) error {
	if opts.StepSeconds < 0 {
		return fmt.Errorf("%w: step_seconds cannot be negative", ErrInvalidQuery)
	}
	return validateTimeRange(opts.StartTime, opts.EndTime)
}

func (h *Queries) ListSpansBySession(ctx context.Context, opts SpanListOptions) (*SpanListResult, error) {
	scope, err := h.authorizeScope(ctx, WorkScope{WorkCaller: opts.WorkCaller, AgentWorkID: opts.AgentWorkID}, true)
	if errors.Is(err, errWorkNotObserved) {
		return &SpanListResult{}, nil
	}
	if err != nil {
		return nil, err
	}
	opts.WorkCaller, opts.AgentWorkID = scope.WorkCaller, scope.AgentWorkID
	return h.evidence.ListBySession(ctx, opts)
}

// ListSpansByOwner lists an Extension Work Caller's spans carrying the trace
// attributes owner_context and owner_id.
func (h *Queries) ListSpansByOwner(ctx context.Context, opts SpanListOptions) (*SpanListResult, error) {
	var err error
	if opts.WorkCaller, err = extensionCaller(opts.WorkCaller); err != nil {
		return nil, err
	}
	if opts.OwnerContext, err = required(opts.OwnerContext, "owner_context"); err != nil {
		return nil, err
	}
	if opts.OwnerID, err = required(opts.OwnerID, "owner_id"); err != nil {
		return nil, err
	}
	opts.RuntimePurpose = strings.TrimSpace(opts.RuntimePurpose)
	if err = h.authorizeWorkCaller(ctx, opts.WorkCaller); err != nil {
		return nil, err
	}
	opts.AgentWorkID, opts.WorkIDs = "", nil
	return h.evidence.ListByWorkCaller(ctx, opts)
}

// GetSpan looks a span up within the authorized Work Caller, and within the
// authorized Work when one is named.
func (h *Queries) GetSpan(ctx context.Context, identity SpanIdentity) (*Span, error) {
	var err error
	if identity.TraceID, err = required(identity.TraceID, "trace_id"); err != nil {
		return nil, err
	}
	if identity.SpanID, err = required(identity.SpanID, "span_id"); err != nil {
		return nil, err
	}
	scope, err := h.authorizeScope(ctx, WorkScope{WorkCaller: identity.WorkCaller, AgentWorkID: identity.AgentWorkID}, false)
	if errors.Is(err, errWorkNotObserved) {
		return nil, ErrSpanNotFound
	}
	if err != nil {
		return nil, err
	}
	identity.WorkCaller, identity.AgentWorkID = scope.WorkCaller, scope.AgentWorkID
	span, err := h.evidence.GetByIdentity(ctx, identity)
	if err != nil {
		return nil, err
	}
	if span == nil {
		return nil, ErrSpanNotFound
	}
	if span.WorkCaller != identity.WorkCaller || span.TraceID != identity.TraceID || span.SpanID != identity.SpanID ||
		(identity.AgentWorkID != "" && span.AgentWorkID != identity.AgentWorkID) {
		return nil, ErrAuthorizationPermissionDenied
	}
	return span, nil
}

func (h *Queries) GetSessionTraceMetrics(ctx context.Context, opts TraceMetricOptions) (*TraceMetricResult, error) {
	if err := validateMetricOptions(opts); err != nil {
		return nil, err
	}
	scope, err := h.authorizeScope(ctx, WorkScope{WorkCaller: opts.WorkCaller, AgentWorkID: opts.AgentWorkID}, true)
	if errors.Is(err, errWorkNotObserved) {
		return &TraceMetricResult{}, nil
	}
	if err != nil {
		return nil, err
	}
	opts.WorkCaller, opts.AgentWorkID, opts.WorkKey, opts.WorkIDs = scope.WorkCaller, scope.AgentWorkID, WorkKey{}, nil
	return h.measurements.GetBySession(ctx, opts)
}

// GetWorkCallerTraceMetrics reads the usage of an Extension Work Caller's
// Works, narrowed to one Work Key when named, under the access decision of
// the extension defining its kind.
func (h *Queries) GetWorkCallerTraceMetrics(ctx context.Context, opts TraceMetricOptions) (*TraceMetricResult, error) {
	if err := validateMetricOptions(opts); err != nil {
		return nil, err
	}
	var err error
	if opts.WorkCaller, err = extensionCaller(opts.WorkCaller); err != nil {
		return nil, err
	}
	if opts.WorkKey, err = requestedWorkKey(opts.WorkKey); err != nil {
		return nil, err
	}
	if err := h.authorizeWorkCaller(ctx, opts.WorkCaller); err != nil {
		return nil, err
	}
	opts.AgentWorkID, opts.WorkIDs = "", nil
	return h.measurements.GetByWorkCaller(ctx, opts)
}

// GetPersonalTraceMetrics reads the authenticated person's own usage across
// every Work they own, whatever its Work Caller, optionally narrowed to one;
// it is never another person's.
func (h *Queries) GetPersonalTraceMetrics(ctx context.Context, opts TraceMetricOptions) (*TraceMetricResult, error) {
	if err := validateMetricOptions(opts); err != nil {
		return nil, err
	}
	reader, err := person(ctx)
	if err != nil {
		return nil, err
	}
	opts.WorkCaller, opts.WorkKey, opts.WorkIDs, opts.AgentWorkID = WorkCaller{}, WorkKey{}, nil, strings.TrimSpace(opts.AgentWorkID)
	return h.measurements.GetByPerson(ctx, reader, opts)
}
