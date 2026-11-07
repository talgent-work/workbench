package connectrpc

import (
	"context"
	"testing"
	"time"

	tracepublicv1 "github.com/talgent-work/contracts/gen/go/trace/public/v1"

	"connectrpc.com/connect/v2"

	"github.com/talgent-work/workbench/internal/business/trace/application/query"
	"github.com/talgent-work/workbench/internal/pkg/authn"
)

// A Work without a Project is read under Agent's Work access, never the
// Project access decision, and only its evidence without a Project is read.
func TestWorkWithoutProjectIsReadUnderWorkAccess(t *testing.T) {
	authority := &fakeTraceAuthorizer{}
	var spanScope query.SpanListOptions
	var metricScope query.TraceMetricOptions
	var eventScope query.EventOptions
	var spanIdentity query.SpanIdentity
	app := newTestApplicationWithEvents(fakeSpanQueryRepository{
		listBySessionFn: func(_ context.Context, opts query.SpanListOptions) (*query.SpanListResult, error) {
			spanScope = opts
			return &query.SpanListResult{}, nil
		},
		getFn: func(_ context.Context, identity query.SpanIdentity) (*query.Span, error) {
			spanIdentity = identity
			return &query.Span{TraceID: "trace-1", SpanID: "span-1", AgentWorkID: "work-1"}, nil
		},
	}, fakeTraceMetricRepository{getBySessionFn: func(_ context.Context, opts query.TraceMetricOptions) (*query.TraceMetricResult, error) {
		metricScope = opts
		return &query.TraceMetricResult{}, nil
	}}, fakeEventRepository{listFn: func(_ context.Context, opts query.EventOptions) (*query.EventResult, error) {
		eventScope = opts
		return &query.EventResult{}, nil
	}}, authority)
	ctx := authn.WithAuthenticatedUser(context.Background(), authn.AuthenticatedUser{ID: "owner"})

	_, err := app.ListSpansBySession(ctx, &tracepublicv1.ListSpansBySessionRequest{AgentWorkId: "work-1"})
	if err != nil {
		t.Fatalf("ListSpansBySession: %v", err)
	}
	_, err = app.GetSessionTraceMetrics(ctx, &tracepublicv1.GetSessionTraceMetricsRequest{AgentWorkId: "work-1"})
	if err != nil {
		t.Fatalf("GetSessionTraceMetrics: %v", err)
	}
	_, err = app.ListEvents(ctx, &tracepublicv1.ListEventsRequest{AgentWorkId: "work-1"})
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	_, err = app.GetSpan(ctx, &tracepublicv1.GetSpanRequest{AgentWorkId: "work-1", TraceId: "trace-1", SpanId: "span-1"})
	if err != nil {
		t.Fatalf("GetSpan: %v", err)
	}
	if authority.workActor != "owner" || authority.workID != "work-1" || authority.projectID != "" {
		t.Fatalf("authority asked = %+v, want Work access for owner on work-1 only", authority)
	}
	if spanScope.ProjectID != "" || metricScope.ProjectID != "" || eventScope.ProjectID != "" || spanIdentity.ProjectID != "" || spanIdentity.AgentWorkID != "work-1" {
		t.Fatalf("scopes = %+v %+v %+v %+v, want the Work's evidence without a Project", spanScope, metricScope, eventScope, spanIdentity)
	}
}

// Someone who is neither the owner nor a Work Share holder reads nothing of a
// Work without a Project, and nothing is queried on their behalf.
func TestWorkAccessRefusalDisclosesNothing(t *testing.T) {
	authority := &fakeTraceAuthorizer{workErr: query.ErrAuthorizationPermissionDenied}
	app := newTestApplicationWithEvents(fakeSpanQueryRepository{
		listBySessionFn: func(context.Context, query.SpanListOptions) (*query.SpanListResult, error) {
			t.Fatal("spans read for a refused reader")
			return nil, nil
		},
		getFn: func(context.Context, query.SpanIdentity) (*query.Span, error) {
			t.Fatal("span read for a refused reader")
			return nil, nil
		},
	}, fakeTraceMetricRepository{getBySessionFn: func(context.Context, query.TraceMetricOptions) (*query.TraceMetricResult, error) {
		t.Fatal("metrics read for a refused reader")
		return nil, nil
	}}, fakeEventRepository{listFn: func(context.Context, query.EventOptions) (*query.EventResult, error) {
		t.Fatal("events read for a refused reader")
		return nil, nil
	}}, authority)
	ctx := authn.WithAuthenticatedUser(context.Background(), authn.AuthenticatedUser{ID: "stranger"})
	for name, call := range map[string]func() error{
		"spans": func() error {
			_, err := app.ListSpansBySession(ctx, &tracepublicv1.ListSpansBySessionRequest{AgentWorkId: "work-1"})
			return err
		},
		"metrics": func() error {
			_, err := app.GetSessionTraceMetrics(ctx, &tracepublicv1.GetSessionTraceMetricsRequest{AgentWorkId: "work-1"})
			return err
		},
		"events": func() error {
			_, err := app.ListEvents(ctx, &tracepublicv1.ListEventsRequest{AgentWorkId: "work-1"})
			return err
		},
		"span": func() error {
			_, err := app.GetSpan(ctx, &tracepublicv1.GetSpanRequest{AgentWorkId: "work-1", TraceId: "trace-1", SpanId: "span-1"})
			return err
		},
	} {
		if connect.CodeOf(call()) != connect.CodePermissionDenied {
			t.Errorf("%s: want PermissionDenied", name)
		}
	}
}

// Without a Project the Work names the scope: queries naming neither are
// refused before any authority or evidence is consulted, and a span fetched
// under one Work is not disclosed as another's.
func TestQueriesWithoutProjectNameTheWork(t *testing.T) {
	authority := &fakeTraceAuthorizer{}
	app := newTestApplicationWithEvents(fakeSpanQueryRepository{getFn: func(context.Context, query.SpanIdentity) (*query.Span, error) {
		return &query.Span{TraceID: "trace-1", SpanID: "span-1", AgentWorkID: "another-work"}, nil
	}}, fakeTraceMetricRepository{}, fakeEventRepository{}, authority)
	ctx := authn.WithAuthenticatedUser(context.Background(), authn.AuthenticatedUser{ID: "owner"})
	_, err := app.ListEvents(ctx, &tracepublicv1.ListEventsRequest{})
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("events naming neither: %v", err)
	}
	_, err = app.GetSpan(ctx, &tracepublicv1.GetSpanRequest{TraceId: "trace-1", SpanId: "span-1"})
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("span naming neither: %v", err)
	}
	if authority.workID != "" || authority.projectID != "" {
		t.Fatalf("authority consulted for an unscoped query: %+v", authority)
	}
	_, err = app.GetSpan(ctx, &tracepublicv1.GetSpanRequest{AgentWorkId: "work-1", TraceId: "trace-1", SpanId: "span-1"})
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("another Work's span: %v", err)
	}
}

// A person reads only their own usage: the person is the authenticated
// reader, never a request field, and no access decision is asked.
func TestPersonalMetricsReadTheReadersOwnUsage(t *testing.T) {
	authority := &fakeTraceAuthorizer{projectErr: query.ErrAuthorizationPermissionDenied, workErr: query.ErrAuthorizationPermissionDenied}
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	var person string
	var captured query.TraceMetricOptions
	app := newTestApplicationWithMetrics(fakeSpanQueryRepository{}, fakeTraceMetricRepository{
		getByPersonFn: func(_ context.Context, personID string, opts query.TraceMetricOptions) (*query.TraceMetricResult, error) {
			person, captured = personID, opts
			return &query.TraceMetricResult{Buckets: []query.TraceMetricBucket{{AgentWorkID: "work-1", UserID: personID, MetricKind: "input_tokens", Value: 7}},
				Totals: []query.TraceMetricTotal{{MetricKind: "input_tokens", Value: 7}}}, nil
		},
	}, authority)
	ctx := authn.WithAuthenticatedUser(context.Background(), authn.AuthenticatedUser{ID: "person-1"})
	response, err := app.GetPersonalTraceMetrics(ctx, &tracepublicv1.GetPersonalTraceMetricsRequest{
		StartTime: timestampOrNil(start), EndTime: timestampOrNil(start.Add(time.Hour)), StepSeconds: 300, AgentWorkId: " work-1 ",
	})
	if err != nil {
		t.Fatalf("GetPersonalTraceMetrics: %v", err)
	}
	if person != "person-1" || captured.AgentWorkID != "work-1" || captured.StepSeconds != 300 || !captured.StartTime.Equal(start) {
		t.Fatalf("read person %q with %+v", person, captured)
	}
	if authority.projectID != "" || authority.workID != "" {
		t.Fatalf("an access decision was asked: %+v", authority)
	}
	if got := response.GetBuckets()[0]; got.GetAgentWorkId() != "work-1" || got.GetValue() != 7 || response.GetTotals()[0].GetValue() != 7 {
		t.Fatalf("response = %+v", response)
	}
	_, err = app.GetPersonalTraceMetrics(context.Background(), &tracepublicv1.GetPersonalTraceMetricsRequest{})
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("anonymous personal usage: %v", err)
	}
}
