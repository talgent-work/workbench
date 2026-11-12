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
	// ApplicationID names the Application whose Works' spans an application
	// listing reads; WorkIDs, when not nil, narrows it to those Works.
	ApplicationID string
	WorkIDs       []string
	Limit         int
	AfterCursor   string
	NewestFirst   bool
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

// ApplicationMetricOptions narrows an Application's measurements to WorkIDs
// when not nil; an empty, non-nil WorkIDs matches nothing.
type ApplicationMetricOptions struct {
	TraceMetricOptions
	WorkIDs []string
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
	ApplicationID  string
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
	ListBySession(ctx context.Context, opts SpanListOptions) (*SpanListResult, error)
	ListByOwner(ctx context.Context, opts SpanListOptions) (*SpanListResult, error)
	// ListByApplication lists the spans of an Application's Works, narrowed
	// to opts.WorkIDs when not nil.
	ListByApplication(ctx context.Context, opts SpanListOptions) (*SpanListResult, error)

	// GetByIdentity fetches one native span within its Trace and Project.
	// Returns (nil, nil) if not found — query flows surface "not found" via
	// the Application/Interface layer, not as an error here.
	GetByIdentity(ctx context.Context, identity SpanIdentity) (*Span, error)

	ListEvents(ctx context.Context, opts EventOptions) (*EventResult, error)
}

// MeasurementQueryRepository reads derived measurements without rescanning
// native evidence.
type MeasurementQueryRepository interface {
	GetBySession(ctx context.Context, opts TraceMetricOptions) (*TraceMetricResult, error)
	GetByProject(ctx context.Context, opts TraceMetricOptions) (*TraceMetricResult, error)
	// GetByPerson reads the measurements of a person's Works, ignoring the
	// Project and narrowing to opts.AgentWorkID when set.
	GetByPerson(ctx context.Context, personID string, opts TraceMetricOptions) (*TraceMetricResult, error)
	// GetByApplication reads the measurements of an Application's Works.
	GetByApplication(ctx context.Context, applicationID string, opts ApplicationMetricOptions) (*TraceMetricResult, error)
	// ListApplicationWorks lists the Works an Application has measurements
	// of within the window, at most limit.
	ListApplicationWorks(ctx context.Context, applicationID string, start, end time.Time, limit int) ([]string, error)
}
