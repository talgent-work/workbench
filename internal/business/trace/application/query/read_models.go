package query

import (
	"context"
	"errors"
	"time"
)

// ErrInvalidSpanCursor marks malformed cursor input so RPC handlers can
// return InvalidArgument instead of treating it as infrastructure failure.
var ErrInvalidSpanCursor = errors.New("invalid span cursor")

// SpanListOptions names one Work's spans within its Work Caller, or a Work
// Caller's spans, narrowed by the trace attributes RuntimePurpose,
// OwnerContext and OwnerID when set and to WorkIDs when not nil.
type SpanListOptions struct {
	WorkCaller     WorkCaller
	AgentWorkID    string
	RuntimePurpose string
	OwnerContext   string
	OwnerID        string
	WorkIDs        []string
	Limit          int
	AfterCursor    string
	NewestFirst    bool
}

type SpanListResult struct {
	Spans      []*Span
	NextCursor string
}

// SpanIdentity names a span within its Work Caller, and within its Work when
// AgentWorkID is set.
type SpanIdentity struct {
	WorkCaller                   WorkCaller
	AgentWorkID, TraceID, SpanID string
}

type Span struct {
	SpanID              string
	TraceID             string
	ParentSpanID        string
	WorkCaller          WorkCaller
	WorkKey             WorkKey
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

// TraceMetricOptions names a window of measurements. A Work's read names
// its Work Caller and AgentWorkID; a Work Caller's read narrows to WorkKey
// when set and to WorkIDs when not nil, where an empty, non-nil WorkIDs
// matches nothing; a person's read narrows to AgentWorkID when set.
type TraceMetricOptions struct {
	WorkCaller  WorkCaller
	WorkKey     WorkKey
	AgentWorkID string
	WorkIDs     []string
	StartTime   time.Time
	EndTime     time.Time
	StepSeconds int
	MetricKinds []string
}

type TraceMetricBucket struct {
	BucketStart    time.Time
	BucketSeconds  int
	WorkCaller     WorkCaller
	WorkKey        WorkKey
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

// Totals sums buckets per metric kind and dimension, in first-seen order;
// unknown contributions stay apart from the sum of known values.
func Totals(buckets []TraceMetricBucket) []TraceMetricTotal {
	type key struct{ metricKind, dimension string }
	index := map[key]int{}
	var totals []TraceMetricTotal
	for _, bucket := range buckets {
		k := key{bucket.MetricKind, bucket.Dimension}
		i, ok := index[k]
		if !ok {
			i = len(totals)
			index[k] = i
			totals = append(totals, TraceMetricTotal{MetricKind: k.metricKind, Dimension: k.dimension})
		}
		totals[i].Value += bucket.Value
		totals[i].UnknownCount += bucket.UnknownCount
	}
	return totals
}

// NativeEvidenceQueryRepository reads retained native evidence: spans and
// allowlisted log events with their Work attribution.
type NativeEvidenceQueryRepository interface {
	// ListBySession lists one Work's spans within its Work Caller.
	ListBySession(ctx context.Context, opts SpanListOptions) (*SpanListResult, error)
	// ListByWorkCaller lists a Work Caller's spans, narrowed by the trace
	// attributes and Works opts names.
	ListByWorkCaller(ctx context.Context, opts SpanListOptions) (*SpanListResult, error)

	// GetByIdentity fetches one native span within its Trace and Work Caller.
	// Returns (nil, nil) if not found — query flows surface "not found" via
	// the Application/Interface layer, not as an error here.
	GetByIdentity(ctx context.Context, identity SpanIdentity) (*Span, error)

	ListEvents(ctx context.Context, opts EventOptions) (*EventResult, error)
}

// MeasurementQueryRepository reads derived measurements without rescanning
// native evidence.
type MeasurementQueryRepository interface {
	// GetBySession reads one Work's measurements within its Work Caller.
	GetBySession(ctx context.Context, opts TraceMetricOptions) (*TraceMetricResult, error)
	// GetByWorkCaller reads the measurements of a Work Caller's Works,
	// narrowed to opts.WorkKey and opts.WorkIDs.
	GetByWorkCaller(ctx context.Context, opts TraceMetricOptions) (*TraceMetricResult, error)
	// GetByPerson reads the measurements of every Work a person owns,
	// whatever its Work Caller, narrowed to opts.AgentWorkID when set.
	GetByPerson(ctx context.Context, personID string, opts TraceMetricOptions) (*TraceMetricResult, error)
	// ListWorkCallerWorks lists the Works a Work Caller has measurements of
	// within the window, at most limit.
	ListWorkCallerWorks(ctx context.Context, caller WorkCaller, start, end time.Time, limit int) ([]string, error)
	// GetWorkCaller reads the one Work Caller a Work's retained measurements
	// carry, ignoring measurements without one, with false when none carries
	// one; measurements carrying different Work Callers are an error.
	GetWorkCaller(ctx context.Context, workID string) (WorkCaller, bool, error)
}
