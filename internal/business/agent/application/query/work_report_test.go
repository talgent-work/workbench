package query

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

type reportSource struct {
	WorkQueryRepository
	works  []WorkHistory
	scope  WorkReportScope
	called bool
	err    error
}

func (r *reportSource) ReadWorkHistory(_ context.Context, scope WorkReportScope, _ time.Time) ([]WorkHistory, error) {
	r.called = true
	r.scope = scope
	return r.works, r.err
}

var projectScope = WorkReportScope{WorkCaller: domain.WorkCaller{Kind: "project", ID: "project"}, Purpose: "work"}

func TestWorkReportUsesHistoricalEligibilityAndPermanentClosure(t *testing.T) {
	end := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	start := end.Add(-24 * time.Hour)
	fact := func(revision int, at time.Time, state domain.WorkState, started, failed bool) WorkStateFact {
		return WorkStateFact{Revision: revision, At: at, State: state, Started: started, Failed: failed}
	}
	source := &reportSource{works: []WorkHistory{
		{WorkID: "closed", AgentOwner: "person", CreatedAt: start.Add(-time.Hour), Facts: []WorkStateFact{
			fact(1, start.Add(-time.Hour), domain.WorkStarting, false, false),
			fact(2, start.Add(-30*time.Minute), domain.WorkWorking, true, false),
			fact(3, start.Add(30*time.Minute), domain.WorkClosing, true, false),
			fact(4, start.Add(time.Hour), domain.WorkClosed, true, false)}},
		{WorkID: "failed-then-closed", AgentOwner: "person", CreatedAt: start.Add(time.Hour), Facts: []WorkStateFact{
			fact(1, start.Add(time.Hour), domain.WorkStarting, false, false),
			fact(2, start.Add(2*time.Hour), domain.WorkFailed, false, true),
			fact(3, start.Add(3*time.Hour), domain.WorkClosed, false, true)}},
		{WorkID: "restored", AgentOwner: "person", CreatedAt: start.Add(time.Hour), Facts: []WorkStateFact{
			fact(1, start.Add(time.Hour), domain.WorkStarting, false, false),
			fact(2, start.Add(2*time.Hour), domain.WorkFailed, true, true),
			fact(3, start.Add(3*time.Hour), domain.WorkStarting, false, false),
			fact(4, start.Add(4*time.Hour), domain.WorkWorking, true, false)}},
		{WorkID: "retry-not-started", AgentOwner: "person", CreatedAt: start.Add(time.Hour), Facts: []WorkStateFact{
			fact(1, start.Add(time.Hour), domain.WorkStarting, false, false),
			fact(2, start.Add(2*time.Hour), domain.WorkFailed, false, true),
			fact(3, start.Add(3*time.Hour), domain.WorkStarting, false, false)}},
	}}
	result, err := NewGetWorkReportHandler(source, func() time.Time { return end }).Handle(context.Background(), GetWorkReport{Scope: projectScope, Window: "24h", End: end})
	require.NoError(t, err)
	require.True(t, result.Current.HistoryComplete)
	require.EqualValues(t, 1, *result.Current.Created, "creation count uses Work eligibility at the relevant period end")
	require.EqualValues(t, 1, *result.Current.Active)
	require.EqualValues(t, 1, *result.Current.Closed)
	require.EqualValues(t, 1, *result.Previous.Active, "current Closed must not rewrite the prior Running snapshot")
	require.EqualValues(t, 0, *result.Previous.Closed)
	require.Len(t, result.Current.Buckets, 12)
	require.EqualValues(t, 1, *result.Current.Buckets[0].Closed)
	require.Equal(t, 120.0, *result.Current.Buckets[0].P50, "lifetime starts at Session creation, ends at permanent closure")
	require.Nil(t, result.Current.Buckets[1].P50)
	require.Equal(t, []WorkDistribution{{Key: "person", Count: 2}}, result.Current.Owners)
}

func TestWorkReportWaitingUnknownFailureAndAuthorization(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	created := now.Add(-time.Hour)
	source := &reportSource{works: []WorkHistory{{WorkID: "waiting", CreatedAt: created, Facts: []WorkStateFact{{Revision: 1, At: created, State: domain.WorkStarting}, {Revision: 2, At: created.Add(time.Minute), State: domain.WorkWorking, Started: true, Waiting: true}}}}}
	handler := NewGetWorkReportHandler(source, func() time.Time { return now })
	for _, window := range []struct {
		name  string
		count int
		step  int64
	}{{"24h", 12, 7200}, {"7d", 14, 43200}, {"30d", 15, 172800}} {
		report, err := handler.Handle(context.Background(), GetWorkReport{Scope: projectScope, Window: window.name})
		require.NoError(t, err)
		require.Len(t, report.Current.Buckets, window.count)
		require.Equal(t, window.step, report.StepSeconds)
		require.EqualValues(t, 0, *report.Current.Active)
		require.EqualValues(t, 1, *report.Current.Waiting)
	}
	source.works = append(source.works, WorkHistory{WorkID: "missing-history", CreatedAt: created})
	report, err := handler.Handle(context.Background(), GetWorkReport{Scope: projectScope, Window: "24h"})
	require.NoError(t, err)
	require.False(t, report.Current.HistoryComplete)
	require.Nil(t, report.Current.Created)
	require.EqualValues(t, 0, requireCount(t, report.Current.Buckets[0].Closed), "missing Work cannot close before its creation")
	require.Empty(t, report.Current.States)
	require.True(t, report.Previous.HistoryComplete, "not-yet-created Work does not poison earlier snapshots")
	source.err = errors.New("database unavailable")
	_, err = handler.Handle(context.Background(), GetWorkReport{Scope: projectScope, Window: "24h"})
	require.ErrorIs(t, err, source.err, "query failure is not an empty population")
	source.called = false
	_, err = handler.Handle(context.Background(), GetWorkReport{Window: "24h"})
	require.ErrorIs(t, err, domain.ErrInvalidWork, "an unscoped report of every Work is never served")
	require.False(t, source.called)
}

func TestWorkLifetimePercentilesUseIndividualClosureSamples(t *testing.T) {
	end := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	start := end.Add(-24 * time.Hour)
	closeTime := start.Add(2 * time.Hour) // Exactly the next bucket boundary.
	histories := []WorkHistory{}
	for _, minutes := range []int{10, 20, 40, 100} {
		created := closeTime.Add(-time.Duration(minutes) * time.Minute)
		histories = append(histories, WorkHistory{CreatedAt: created, Facts: []WorkStateFact{
			{Revision: 1, At: created, State: domain.WorkStarting},
			{Revision: 2, At: closeTime, State: domain.WorkClosed},
		}})
	}
	// Retained failure evidence excludes a legacy closed Work even if its older
	// exact closure time is unknown. Closure cannot restore its eligibility.
	histories = append(histories, WorkHistory{CreatedAt: start.Add(-time.Hour), Facts: []WorkStateFact{{Revision: 50, At: start, State: domain.WorkClosed, Failed: true}}})
	report := buildWorkPeriod(histories, start, end, 2*time.Hour, 12, false)
	require.True(t, report.HistoryComplete)
	require.EqualValues(t, 0, *report.Buckets[0].Closed)
	require.EqualValues(t, 4, *report.Buckets[1].Closed)
	require.Equal(t, 30.0, *report.Buckets[1].P50)
	require.Equal(t, 55.0, *report.Buckets[1].P75)
	require.InDelta(t, 82.0, *report.Buckets[1].P90, .000001)
	require.EqualValues(t, 4, *report.Closed)
}

func TestWorkReportListsEligibleWorksForTheCallersOwnClassification(t *testing.T) {
	end := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	created := end.Add(-time.Hour)
	source := &reportSource{works: []WorkHistory{
		{WorkID: "review", CreatedAt: created, Facts: []WorkStateFact{{Revision: 1, At: created, State: domain.WorkWorking, Started: true}}},
		{WorkID: "done", CreatedAt: created, Facts: []WorkStateFact{{Revision: 1, At: created, State: domain.WorkStarting}, {Revision: 2, At: created.Add(time.Minute), State: domain.WorkClosed}}},
		{WorkID: "failed", CreatedAt: created, Facts: []WorkStateFact{{Revision: 1, At: created, State: domain.WorkFailed, Failed: true}}},
	}}
	report, err := NewGetWorkReportHandler(source, func() time.Time { return end }).Handle(context.Background(), GetWorkReport{Scope: projectScope, Window: "24h", End: end})
	require.NoError(t, err)
	require.Equal(t, projectScope, source.scope, "the caller's generic scope selects the Works")
	require.True(t, report.Current.HistoryComplete)
	require.ElementsMatch(t, []string{"review", "done"}, report.Current.EligibleWorkIDs, "failed Work is never eligible")
}

func TestWorkReportMissingClosureTimeDoesNotEraseKnownStateOrCreation(t *testing.T) {
	end := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	start := end.Add(-24 * time.Hour)
	// A retained observation proves closure and successful execution, but not
	// when closure happened. Creation and current state remain authoritative.
	works := []WorkHistory{{WorkID: "closed", AgentOwner: "person", CreatedAt: start.Add(3 * time.Hour), Facts: []WorkStateFact{
		{Revision: 20, At: start.Add(5 * time.Hour), State: domain.WorkClosed, Started: true},
	}}}
	report := buildWorkPeriod(works, start, end, 2*time.Hour, 12, false)
	require.EqualValues(t, 1, requireCount(t, report.Created))
	require.EqualValues(t, 1, requireCount(t, report.Closed))
	require.EqualValues(t, 0, requireCount(t, report.Active))
	require.Equal(t, []WorkDistribution{{Key: "closed", Count: 1}}, report.States)
	require.EqualValues(t, 1, requireCount(t, report.Buckets[1].Created))
	require.EqualValues(t, 0, requireCount(t, report.Buckets[0].Closed), "cannot close before creation")
	require.Nil(t, report.Buckets[1].Closed)
	require.Nil(t, report.Buckets[2].Closed)
	require.Nil(t, report.Buckets[2].P50)
	require.EqualValues(t, 0, requireCount(t, report.Buckets[3].Closed), "already observed Closed before this bucket")
}

func requireCount(t *testing.T, count *int64) int64 {
	t.Helper()
	require.NotNil(t, count, "known counts must survive unrelated evidence gaps")
	return *count
}

func TestWorkReportLiveStateNeverReconstructsPreviousPeriod(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	source := &reportSource{works: []WorkHistory{
		{WorkID: "working", CreatedAt: now.Add(-48 * time.Hour), Current: &WorkStateFact{Revision: 30, State: domain.WorkWorking, Started: true}},
		{WorkID: "closed", CreatedAt: now.Add(-48 * time.Hour), Current: &WorkStateFact{Revision: 40, State: domain.WorkClosed, Started: true}},
		{WorkID: "failed", CreatedAt: now.Add(-48 * time.Hour), Current: &WorkStateFact{Revision: 50, State: domain.WorkClosed, Failed: true}},
	}}
	handler := NewGetWorkReportHandler(source, func() time.Time { return now })
	report, err := handler.Handle(context.Background(), GetWorkReport{Scope: projectScope, Window: "24h"})
	require.NoError(t, err)
	require.True(t, report.Current.HistoryComplete)
	require.EqualValues(t, 0, requireCount(t, report.Current.Created))
	require.EqualValues(t, 1, requireCount(t, report.Current.Active))
	require.EqualValues(t, 0, requireCount(t, report.Current.Waiting))
	require.EqualValues(t, 1, requireCount(t, report.Current.Closed))
	require.Nil(t, report.Current.Buckets[0].Closed, "live state does not invent a closure time")
	require.Nil(t, report.Previous.Active, "live state does not establish yesterday's eligibility or state")
	require.EqualValues(t, 0, requireCount(t, report.Previous.Created), "creation before the preceding window is still known")
	require.ElementsMatch(t, []string{"working", "closed"}, report.Current.EligibleWorkIDs)
	report, err = handler.Handle(context.Background(), GetWorkReport{Scope: projectScope, Window: "24h", End: now.Add(-2 * time.Hour)})
	require.NoError(t, err)
	require.Nil(t, report.Current.Active, "explicit past period cannot consume live state")
}

func TestWorkReportUnknownEligibilityOnlyAffectsRelevantCountersAndBuckets(t *testing.T) {
	end := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	start := end.Add(-24 * time.Hour)
	works := []WorkHistory{{WorkID: "unknown-prior-failure", CreatedAt: start.Add(-time.Hour), Facts: []WorkStateFact{
		{Revision: 20, At: start.Add(time.Hour), State: domain.WorkStopped},
		{Revision: 21, At: start.Add(3 * time.Hour), State: domain.WorkClosed},
	}}}
	report := buildWorkPeriod(works, start, end, 2*time.Hour, 12, false)
	require.EqualValues(t, 0, requireCount(t, report.Created))
	require.EqualValues(t, 0, requireCount(t, report.Active))
	require.EqualValues(t, 0, requireCount(t, report.Waiting))
	require.Nil(t, report.Closed)
	require.False(t, report.HistoryComplete)
	require.Empty(t, report.States)
	for i, bucket := range report.Buckets {
		require.EqualValues(t, 0, requireCount(t, bucket.Created))
		if i == 1 {
			require.Nil(t, bucket.Closed)
		} else {
			require.EqualValues(t, 0, requireCount(t, bucket.Closed))
		}
	}
}
