package query

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
)

// maxApplicationWorks bounds the Works one label filter or breakdown resolves
// through Agent; a wider window is refused rather than read in part.
const maxApplicationWorks = 5000

// maxLabelFilter bounds the Work Labels one read narrows by, as a Work
// carries at most this many.
const maxLabelFilter = 16

// nativeEvidenceWindow is how long native evidence is kept.
const nativeEvidenceWindow = 7 * 24 * time.Hour

// ApplicationUsageOptions reads an Application's measurements: those of its
// Works carrying every one of Labels, broken down by the values of
// BreakdownKey when set, with the label keys its measured Works carry when
// ListLabelKeys.
type ApplicationUsageOptions struct {
	ApplicationID string
	TraceMetricOptions
	Labels        map[string]string
	BreakdownKey  string
	ListLabelKeys bool
}

// LabelUsage is the usage of the measured Works carrying one value of the
// breakdown key, or, with Labelled false, of those without the key.
type LabelUsage struct {
	Value     string
	Labelled  bool
	Totals    []TraceMetricTotal
	WorkCount int
}

// ApplicationUsage is an Application's usage with its breakdown and the
// label keys its measured Works carry.
type ApplicationUsage struct {
	TraceMetricResult
	Breakdown []LabelUsage
	LabelKeys []string
}

// ApplicationSpanOptions lists the spans of an Application's Works carrying
// every one of Labels, newest first.
type ApplicationSpanOptions struct {
	ApplicationID string
	Labels        map[string]string
	Limit         int
	AfterCursor   string
}

// GetApplicationTraceMetrics reads an Application's usage, that of the Works
// whose Work Caller it is, for the Application or its Application Owner
// (Application usage access), narrowed and broken down by Work Labels that
// Agent resolves to Works; Observability keeps no label (Usage by Work
// Label).
func (q *Queries) GetApplicationTraceMetrics(ctx context.Context, opts ApplicationUsageOptions) (*ApplicationUsage, error) {
	if err := validateMetricOptions(opts.TraceMetricOptions); err != nil {
		return nil, err
	}
	labels, err := labelFilter(opts.Labels)
	if err != nil {
		return nil, err
	}
	breakdownKey := strings.TrimSpace(opts.BreakdownKey)
	application, err := q.authorizeApplication(ctx, opts.ApplicationID)
	if err != nil {
		return nil, err
	}
	metricOptions := opts.TraceMetricOptions
	metricOptions.WorkCaller, metricOptions.WorkKey, metricOptions.AgentWorkID, metricOptions.WorkIDs = ApplicationCaller(application), WorkKey{}, "", nil
	var attributed map[string]map[string]string
	usage := &ApplicationUsage{}
	if len(labels) > 0 || breakdownKey != "" || opts.ListLabelKeys {
		if attributed, err = q.attributedWorks(ctx, application, opts.StartTime, opts.EndTime); err != nil {
			return nil, err
		}
		metricOptions.WorkIDs = carrying(attributed, labels)
		if opts.ListLabelKeys {
			usage.LabelKeys = labelKeys(attributed, metricOptions.WorkIDs)
		}
		if len(metricOptions.WorkIDs) == 0 {
			return usage, nil
		}
	}
	result, err := q.measurements.GetByWorkCaller(ctx, metricOptions)
	if err != nil {
		return nil, err
	}
	usage.TraceMetricResult = *result
	if breakdownKey != "" {
		usage.Breakdown = breakdown(result.Buckets, attributed, breakdownKey)
	}
	return usage, nil
}

// ListSpansByApplication lists the spans of an Application's Works for the
// Application or its Application Owner, narrowed by Work Labels that Agent
// resolves to the Works with native evidence still kept.
func (q *Queries) ListSpansByApplication(ctx context.Context, opts ApplicationSpanOptions) (*SpanListResult, error) {
	labels, err := labelFilter(opts.Labels)
	if err != nil {
		return nil, err
	}
	application, err := q.authorizeApplication(ctx, opts.ApplicationID)
	if err != nil {
		return nil, err
	}
	list := SpanListOptions{WorkCaller: ApplicationCaller(application), Limit: opts.Limit, AfterCursor: opts.AfterCursor, NewestFirst: true}
	if len(labels) > 0 {
		attributed, err := q.attributedWorks(ctx, application, time.Now().Add(-nativeEvidenceWindow), time.Time{})
		if err != nil {
			return nil, err
		}
		if list.WorkIDs = carrying(attributed, labels); len(list.WorkIDs) == 0 {
			return &SpanListResult{}, nil
		}
	}
	return q.evidence.ListByWorkCaller(ctx, list)
}

// attributedWorks resolves the Works measured in the window whose Work Caller
// is the Application to their Work Labels through Agent, which answers only
// for the Works the Application owns; a measured Work Agent does not
// attribute to it is left out.
func (q *Queries) attributedWorks(ctx context.Context, application string, start, end time.Time) (map[string]map[string]string, error) {
	works, err := q.measurements.ListWorkCallerWorks(ctx, ApplicationCaller(application), start, end, maxApplicationWorks+1)
	if err != nil {
		return nil, err
	}
	if len(works) > maxApplicationWorks {
		return nil, fmt.Errorf("%w: more than %d Works in the window; narrow it", ErrInvalidQuery, maxApplicationWorks)
	}
	if q.agent == nil {
		return nil, ErrAuthorizationUnavailable
	}
	attributed := make(map[string]map[string]string, len(works))
	for batch := range slices.Chunk(works, MaxAttributedWorks) {
		answered, err := q.agent.ListWorkAttributions(ctx, application, batch)
		if err != nil {
			return nil, err
		}
		for _, work := range answered {
			attributed[work.WorkID] = work.Labels
		}
	}
	return attributed, nil
}

// labelFilter normalizes the Work Labels a read narrows by.
func labelFilter(labels map[string]string) (map[string]string, error) {
	if len(labels) > maxLabelFilter {
		return nil, fmt.Errorf("%w: at most %d labels", ErrInvalidQuery, maxLabelFilter)
	}
	filter := make(map[string]string, len(labels))
	for key, value := range labels {
		if key = strings.TrimSpace(key); key == "" {
			return nil, fmt.Errorf("%w: a label key is required", ErrInvalidQuery)
		}
		filter[key] = value
	}
	return filter, nil
}

// carrying lists the attributed Works carrying every one of labels, sorted.
func carrying(attributed map[string]map[string]string, labels map[string]string) []string {
	works := make([]string, 0, len(attributed))
	for work, carried := range attributed {
		matches := true
		for key, value := range labels {
			if got, ok := carried[key]; !ok || got != value {
				matches = false
				break
			}
		}
		if matches {
			works = append(works, work)
		}
	}
	slices.Sort(works)
	return works
}

// labelKeys are the Work Label keys the listed Works carry, sorted.
func labelKeys(attributed map[string]map[string]string, works []string) []string {
	seen := map[string]bool{}
	for _, work := range works {
		for key := range attributed[work] {
			seen[key] = true
		}
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

// breakdown groups per-Work rollups by the value each Work carries for key,
// the Works without it last; larger groups by total come first.
func breakdown(buckets []TraceMetricBucket, attributed map[string]map[string]string, key string) []LabelUsage {
	type group struct {
		value    string
		labelled bool
	}
	grouped := map[group][]TraceMetricBucket{}
	works := map[group]map[string]bool{}
	for _, bucket := range buckets {
		value, labelled := attributed[bucket.AgentWorkID][key]
		g := group{value, labelled}
		grouped[g] = append(grouped[g], bucket)
		if works[g] == nil {
			works[g] = map[string]bool{}
		}
		works[g][bucket.AgentWorkID] = true
	}
	usage := make([]LabelUsage, 0, len(grouped))
	for g, members := range grouped {
		usage = append(usage, LabelUsage{Value: g.value, Labelled: g.labelled, Totals: Totals(members), WorkCount: len(works[g])})
	}
	slices.SortFunc(usage, func(a, b LabelUsage) int {
		if a.Labelled != b.Labelled {
			if a.Labelled {
				return -1
			}
			return 1
		}
		return cmp.Or(cmp.Compare(tokens(b.Totals), tokens(a.Totals)), cmp.Compare(a.Value, b.Value))
	})
	return usage
}

// tokenKinds are the four token categories whose sum is a Total.
var tokenKinds = []string{"input_tokens", "cache_read_tokens", "cache_creation_tokens", "output_tokens"}

func tokens(totals []TraceMetricTotal) float64 {
	var sum float64
	for _, total := range totals {
		if slices.Contains(tokenKinds, total.MetricKind) {
			sum += total.Value
		}
	}
	return sum
}
