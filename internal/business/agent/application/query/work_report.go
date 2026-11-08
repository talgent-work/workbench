package query

import (
	"context"
	"errors"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

var ErrInvalidWorkReport = errors.New("agent: invalid Work report period")

type WorkStateFact struct {
	Revision        int
	At              time.Time
	State           domain.WorkState
	Started, Failed bool
	// Waiting reports a Work whose user owes the answer to a question.
	Waiting bool
}
type WorkHistory struct {
	WorkID, AgentOwner string
	CreatedAt          time.Time
	Facts              []WorkStateFact
	Current            *WorkStateFact // Live persisted state; never used for a past period.
}

// WorkReportScope selects the reported Works by Project and the definition's
// opaque purpose; the caller authorizes that scope.
type WorkReportScope struct {
	ProjectID string
	Purpose   string
}
type GetWorkReport struct {
	Scope  WorkReportScope
	Window string
	End    time.Time
}
type WorkReportBucket struct {
	Start           time.Time
	Created, Closed *int64
	P50, P75, P90   *float64 // Wall-clock minutes, not execution time.
}
type WorkDistribution struct {
	Key   string
	Count int64
}
type WorkPeriodReport struct {
	Created, Active, Waiting, Closed *int64
	HistoryComplete                  bool
	Buckets                          []WorkReportBucket
	States, Owners                   []WorkDistribution
	// EligibleWorkIDs are the current period's Works a caller may classify by
	// its own facts, such as the status of their Subjects.
	EligibleWorkIDs []string
}
type WorkReport struct {
	Start, End, ObservedAt time.Time
	StepSeconds            int64
	Current, Previous      WorkPeriodReport
}
type GetWorkReportHandler struct {
	reader WorkQueryRepository
	now    func() time.Time
}

func NewGetWorkReportHandler(reader WorkQueryRepository, now func() time.Time) *GetWorkReportHandler {
	return &GetWorkReportHandler{reader: reader, now: now}
}

// Handle reports Works for a trusted caller, which authorizes its own scope.
func (h *GetWorkReportHandler) Handle(ctx context.Context, input GetWorkReport) (WorkReport, error) {
	if strings.TrimSpace(input.Scope.ProjectID) == "" {
		return WorkReport{}, domain.ErrInvalidWork
	}
	var step time.Duration
	var count int
	switch input.Window {
	case "24h":
		step, count = 2*time.Hour, 12
	case "7d":
		step, count = 12*time.Hour, 14
	case "30d":
		step, count = 48*time.Hour, 15
	default:
		return WorkReport{}, ErrInvalidWorkReport
	}
	now := h.now().UTC()
	alignment := 24 * time.Hour
	if input.Window == "24h" {
		alignment = step
	}
	end := input.End.UTC()
	if end.IsZero() {
		end = now.Truncate(alignment).Add(alignment)
	}
	if !end.Equal(end.Truncate(alignment)) || end.After(now.Truncate(alignment).Add(alignment)) || end.Before(time.Unix(0, 0)) {
		return WorkReport{}, ErrInvalidWorkReport
	}
	observed := end
	if now.Before(observed) {
		observed = now
	}
	histories, err := h.reader.ReadWorkHistory(ctx, input.Scope, observed)
	if err != nil {
		return WorkReport{}, err
	}
	start := end.Add(-step * time.Duration(count))
	report := WorkReport{Start: start, End: end, ObservedAt: observed, StepSeconds: int64(step / time.Second),
		Current: buildWorkPeriod(histories, start, observed, step, count, observed.Equal(now)), Previous: buildWorkPeriod(histories, start.Add(-step*time.Duration(count)), start, step, count, false)}
	return report, nil
}
func counter() *int64 { return new(int64) }
func increment(value *int64) {
	if value != nil {
		*value++
	}
}
func buildWorkPeriod(histories []WorkHistory, start, end time.Time, step time.Duration, count int, live bool) WorkPeriodReport {
	result := WorkPeriodReport{Created: counter(), Active: counter(), Waiting: counter(), Closed: counter(), HistoryComplete: true, Buckets: make([]WorkReportBucket, count)}
	samples := make([][]float64, count)
	// Only intervals that could contain an unobserved closure are unavailable.
	missingClosure := func(after, through time.Time) {
		for i := range result.Buckets {
			bucket := &result.Buckets[i]
			if bucket.Start.Before(end) && bucket.Start.Add(step).After(after) && !bucket.Start.After(through) {
				bucket.Closed = nil
			}
		}
	}
	states, owners := map[string]int64{}, map[string]int64{}
	for i := range result.Buckets {
		result.Buckets[i] = WorkReportBucket{Start: start.Add(time.Duration(i) * step), Created: counter(), Closed: counter()}
	}
	for _, work := range histories {
		if !work.CreatedAt.Before(end) {
			continue
		}
		known, eligible := false, false
		var last WorkStateFact
		var closedAt, closureObservedAt time.Time
		for _, fact := range work.Facts {
			if !fact.At.Before(end) {
				break
			}
			// A complete first snapshot establishes initial eligibility. A later
			// successful run or failure supplies fresh evidence even for old Work.
			if fact.Revision == 1 {
				known, eligible = true, true
			}
			if fact.Failed {
				known, eligible = true, false
			} else if fact.Started {
				known, eligible = true, true
			}
			if fact.State == domain.WorkClosed && closureObservedAt.IsZero() {
				closureObservedAt = fact.At
			}
			if fact.State == domain.WorkClosed && last.State != "" && last.State != domain.WorkClosed {
				closedAt = fact.At
			}
			last = fact
		}
		if live && work.Current != nil {
			current := *work.Current
			if current.Revision == 1 || current.Started {
				known, eligible = true, true
			}
			if current.Failed {
				known, eligible = true, false
			}
			// This is a live observation, not a reconstructed transition time.
			current.At = end
			last = current
		}
		if known && !eligible {
			continue
		}
		createdBucket := int(work.CreatedAt.Sub(start) / step)
		if !known || last.State == "" {
			result.HistoryComplete = false
			if last.State == "" || last.State == domain.WorkStarting || last.State == domain.WorkWorking {
				result.Active = nil
			}
			if last.State == "" || last.Waiting {
				result.Waiting = nil
			}
			if !work.CreatedAt.Before(start) {
				result.Created = nil
				if createdBucket >= 0 && createdBucket < count {
					result.Buckets[createdBucket].Created = nil
				}
			}
			if last.State == "" || last.State == domain.WorkClosed {
				result.Closed = nil
				through := end
				if !last.At.IsZero() {
					through = last.At
				}
				if closedAt.IsZero() {
					missingClosure(work.CreatedAt, through)
				} else {
					missingClosure(closedAt, closedAt)
				}
			}
			continue
		}
		if last.State == domain.WorkClosed && closedAt.IsZero() {
			through := closureObservedAt
			if through.IsZero() {
				through = end
			}
			missingClosure(work.CreatedAt, through)
		}

		result.EligibleWorkIDs = append(result.EligibleWorkIDs, work.WorkID)
		// Waiting counts Works whose user owes an answer, stopped ones included;
		// Active counts the other Starting or Working Works, idle ones included.
		if last.Waiting {
			increment(result.Waiting)
		} else if last.State == domain.WorkStarting || last.State == domain.WorkWorking {
			increment(result.Active)
		}
		if last.State == domain.WorkClosed {
			increment(result.Closed)
		}
		states[string(last.State)]++
		owners[work.AgentOwner]++
		if !work.CreatedAt.Before(start) {
			increment(result.Created)
			i := int(work.CreatedAt.Sub(start) / step)
			if i >= 0 && i < count {
				increment(result.Buckets[i].Created)
			}
		}
		if !closedAt.IsZero() && !closedAt.Before(start) {
			i := int(closedAt.Sub(start) / step)
			if i >= 0 && i < count {
				increment(result.Buckets[i].Closed)
				samples[i] = append(samples[i], closedAt.Sub(work.CreatedAt).Minutes())
			}
		}
	}
	for i, values := range samples {
		if len(values) == 0 || result.Buckets[i].Closed == nil {
			continue
		}
		slices.Sort(values)
		result.Buckets[i].P50 = percentile(values, .5)
		result.Buckets[i].P75 = percentile(values, .75)
		result.Buckets[i].P90 = percentile(values, .9)
	}
	if result.HistoryComplete {
		result.States, result.Owners = distribution(states), distribution(owners)
	}
	return result
}
func percentile(values []float64, p float64) *float64 {
	// Continuous linear interpolation over individual Work lifetimes.
	position := float64(len(values)-1) * p
	low, high := int(math.Floor(position)), int(math.Ceil(position))
	result := values[low] + (values[high]-values[low])*(position-float64(low))
	return &result
}
func distribution(counts map[string]int64) []WorkDistribution {
	result := make([]WorkDistribution, 0, len(counts))
	for key, count := range counts {
		result = append(result, WorkDistribution{Key: key, Count: count})
	}
	slices.SortFunc(result, func(a, b WorkDistribution) int {
		if a.Count > b.Count {
			return -1
		}
		if a.Count < b.Count {
			return 1
		}
		return strings.Compare(a.Key, b.Key)
	})
	return result
}

// NewStateObservationID supplies the identity of a retained reporting record.
// Persistence calls it only when a new observation commits with the root.
func NewStateObservationID() string { return uuid.Must(uuid.NewV7()).String() }
