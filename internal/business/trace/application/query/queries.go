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
	projects     ProjectAccessPort
	agent        AgentPort
	accounts     AccountPort
}

// NewQueries takes no Project access where no context decides it.
func NewQueries(evidence NativeEvidenceQueryRepository, measurements MeasurementQueryRepository, projects ProjectAccessPort, agent AgentPort, accounts AccountPort) *Queries {
	return &Queries{evidence: evidence, measurements: measurements, projects: projects, agent: agent, accounts: accounts}
}

var ErrInvalidQuery = errors.New("invalid trace query")
var ErrSpanNotFound = errors.New("span not found")

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
	scope, err := h.authorizeScope(ctx, WorkScope{ProjectID: opts.ProjectID, AgentWorkID: opts.AgentWorkID}, true)
	if err != nil {
		return nil, err
	}
	opts.ProjectID, opts.AgentWorkID = scope.ProjectID, scope.AgentWorkID
	return h.evidence.ListBySession(ctx, opts)
}

func (h *Queries) ListSpansByOwner(ctx context.Context, opts SpanListOptions) (*SpanListResult, error) {
	var err error
	if opts.ProjectID, err = required(opts.ProjectID, "project_id"); err != nil {
		return nil, err
	}
	if opts.OwnerContext, err = required(opts.OwnerContext, "owner_context"); err != nil {
		return nil, err
	}
	if opts.OwnerID, err = required(opts.OwnerID, "owner_id"); err != nil {
		return nil, err
	}
	opts.RuntimePurpose = strings.TrimSpace(opts.RuntimePurpose)
	if err = h.authorizeProject(ctx, opts.ProjectID); err != nil {
		return nil, err
	}
	return h.evidence.ListByOwner(ctx, opts)
}

// GetSpan looks a span up within the authorized Project, or, without one,
// within the authorized Work.
func (h *Queries) GetSpan(ctx context.Context, identity SpanIdentity) (*Span, error) {
	var err error
	if identity.TraceID, err = required(identity.TraceID, "trace_id"); err != nil {
		return nil, err
	}
	if identity.SpanID, err = required(identity.SpanID, "span_id"); err != nil {
		return nil, err
	}
	scope, err := h.authorizeScope(ctx, WorkScope{ProjectID: identity.ProjectID, AgentWorkID: identity.AgentWorkID}, false)
	if err != nil {
		return nil, err
	}
	identity.ProjectID, identity.AgentWorkID = scope.ProjectID, scope.AgentWorkID
	span, err := h.evidence.GetByIdentity(ctx, identity)
	if err != nil {
		return nil, err
	}
	if span == nil {
		return nil, ErrSpanNotFound
	}
	if span.ProjectID != identity.ProjectID || span.TraceID != identity.TraceID || span.SpanID != identity.SpanID ||
		(identity.AgentWorkID != "" && span.AgentWorkID != identity.AgentWorkID) {
		return nil, ErrAuthorizationPermissionDenied
	}
	return span, nil
}

func (h *Queries) GetSessionTraceMetrics(ctx context.Context, opts TraceMetricOptions) (*TraceMetricResult, error) {
	if err := validateMetricOptions(opts); err != nil {
		return nil, err
	}
	scope, err := h.authorizeScope(ctx, WorkScope{ProjectID: opts.ProjectID, AgentWorkID: opts.AgentWorkID}, true)
	if err != nil {
		return nil, err
	}
	opts.ProjectID, opts.AgentWorkID = scope.ProjectID, scope.AgentWorkID
	return h.measurements.GetBySession(ctx, opts)
}

func (h *Queries) GetProjectTraceMetrics(ctx context.Context, opts TraceMetricOptions) (*TraceMetricResult, error) {
	if err := validateMetricOptions(opts); err != nil {
		return nil, err
	}
	var err error
	if opts.ProjectID, err = required(opts.ProjectID, "project_id"); err != nil {
		return nil, err
	}
	if err := h.authorizeProject(ctx, opts.ProjectID); err != nil {
		return nil, err
	}
	return h.measurements.GetByProject(ctx, opts)
}

// GetPersonalTraceMetrics reads the authenticated person's own usage across
// all their Works, optionally narrowed to one; it is never another person's.
func (h *Queries) GetPersonalTraceMetrics(ctx context.Context, opts TraceMetricOptions) (*TraceMetricResult, error) {
	if err := validateMetricOptions(opts); err != nil {
		return nil, err
	}
	reader, err := person(ctx)
	if err != nil {
		return nil, err
	}
	opts.ProjectID, opts.AgentWorkID = "", strings.TrimSpace(opts.AgentWorkID)
	return h.measurements.GetByPerson(ctx, reader, opts)
}
