package query

import (
	"context"
	"errors"
	"time"
)

// ErrInvalidSpanCursor marks malformed cursor input so RPC handlers can
// return InvalidArgument instead of treating it as infrastructure failure.
var ErrInvalidSpanCursor = errors.New("invalid span cursor")

type SpanListOptions struct {
	ProjectID      string
	AgentWorkID    string
	RuntimePurpose string
	OwnerContext   string
	OwnerID        string
	Limit          int
	AfterCursor    string
	NewestFirst    bool
}

type SpanListResult struct {
	Spans      []*Span
	NextCursor string
}

// SpanIdentity names a span within its Project, or, for a Work without a
// Project, within its Work.
type SpanIdentity struct {
	ProjectID, AgentWorkID, TraceID, SpanID string
}

type Span struct {
	SpanID              string
	TraceID             string
	ParentSpanID        string
	ProjectID           string
	AgentWorkID         string
	AgentID             string
	RuntimePurpose      string
	OwnerContext        string
	OwnerID             string
	Cursor              string
	Name                string
	Kind                string
	StartTime           time.Time
	EndTime             time.Time
	DurationNanos       int64
	StatusCode          string
	StatusMessage       string
	Attributes          map[string]string
	ResourceAttributes  map[string]string
	Category            string
	CallID              string
	NativeAgentID       string
	ParentNativeAgentID string
}

type TraceMetricOptions struct {
	ProjectID   string
	AgentWorkID string
	StartTime   time.Time
	EndTime     time.Time
	StepSeconds int
	MetricKinds []string
}

type TraceMetricBucket struct {
	BucketStart    time.Time
	BucketSeconds  int
	ProjectID      string
	AgentWorkID    string
	AgentID        string
	RuntimePurpose string
	OwnerContext   string
	OwnerID        string
	UserID         string
	MetricKind     string
	Dimension      string
	Value          float64
	UnknownCount   uint64
	Gaps           []string
}

type TraceMetricTotal struct {
	MetricKind   string
	Dimension    string
	Value        float64
	UnknownCount uint64
}

type TraceMetricResult struct {
	Buckets []TraceMetricBucket
	Totals  []TraceMetricTotal
}

// SpanQueryRepository is the read-side persistence contract for spans.
// The ClickHouse implementation lives in infrastructure/clickhouse (Task 10).
type SpanQueryRepository interface {
	ListBySession(ctx context.Context, opts SpanListOptions) (*SpanListResult, error)
	ListByOwner(ctx context.Context, opts SpanListOptions) (*SpanListResult, error)

	// GetByIdentity fetches one native span within its Trace and Project.
	// Returns (nil, nil) if not found — query flows surface "not found" via
	// the Application/Interface layer, not as an error here.
	GetByIdentity(ctx context.Context, identity SpanIdentity) (*Span, error)
}

// TraceMetricRepository is the read-side persistence contract for pre-bucketed
// trace metrics. Implementations must read trace_metric_buckets, not raw spans.
type TraceMetricRepository interface {
	GetBySession(ctx context.Context, opts TraceMetricOptions) (*TraceMetricResult, error)
	GetByProject(ctx context.Context, opts TraceMetricOptions) (*TraceMetricResult, error)
	// GetByPerson reads the measurements of a person's Works, ignoring the
	// Project and narrowing to opts.AgentWorkID when set.
	GetByPerson(ctx context.Context, personID string, opts TraceMetricOptions) (*TraceMetricResult, error)
}
