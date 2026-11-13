package connectrpc

import (
	"context"
	"testing"
	"time"

	agentpublicv1 "github.com/talgent-work/contracts/gen/go/agent/public/v1"
	tracepublicv1 "github.com/talgent-work/contracts/gen/go/trace/public/v1"

	"connectrpc.com/connect/v2"

	"github.com/talgent-work/workbench/internal/business/trace/application/query"
	"github.com/talgent-work/workbench/internal/pkg/authn"
)

func TestGetSessionTraceMetrics_UsesMetricRepository(t *testing.T) {
	start := time.Date(2026, 5, 22, 10, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	var captured query.TraceMetricOptions
	app := newTestApplicationWithMetrics(
		fakeSpanQueryRepository{
			listBySessionFn: func(context.Context, query.SpanListOptions) (*query.SpanListResult, error) {
				t.Fatal("ListBySession should not be called for metrics")
				return nil, nil
			},
		},
		fakeTraceMetricRepository{
			getBySessionFn: func(_ context.Context, opts query.TraceMetricOptions) (*query.TraceMetricResult, error) {
				captured = opts
				return &query.TraceMetricResult{
					Buckets: []query.TraceMetricBucket{{
						BucketStart:   start,
						BucketSeconds: 300,
						WorkCaller:    teamCaller,
						WorkKey:       query.WorkKey{Kind: "board", ID: "board-1"},
						AgentWorkID:   "work-1",
						OwnerContext:  "case",
						OwnerID:       "case-alpha",
						UserID:        "user-1",
						MetricKind:    "tool_call",
						Dimension:     "Read",
						Value:         2,
					}},
					Totals: []query.TraceMetricTotal{{MetricKind: "tool_call", Dimension: "Read", Value: 2}},
				}, nil
			},
		},
	)

	resp, err := app.GetSessionTraceMetrics(authn.WithAuthenticatedUser(context.Background(), authn.AuthenticatedUser{ID: "test-reader"}),
		&tracepublicv1.GetSessionTraceMetricsRequest{WorkCaller: &agentpublicv1.WorkCaller{Kind: "team", Id: "team-1"},
			AgentWorkId: " work-1 ",
			StartTime:   timestampOrNil(start),
			EndTime:     timestampOrNil(end),
			StepSeconds: 300,
			MetricKinds: []string{"tool_call"},
		})
	if err != nil {
		t.Fatalf("GetSessionTraceMetrics: %v", err)
	}
	if captured.AgentWorkID != "work-1" || !captured.StartTime.Equal(start) || !captured.EndTime.Equal(end) || captured.StepSeconds != 300 {
		t.Fatalf("captured options = %+v", captured)
	}
	if got := resp.GetBuckets()[0]; got.GetMetricKind() != "tool_call" || got.GetDimension() != "Read" || got.GetValue() != 2 {
		t.Fatalf("bucket = %+v", got)
	}
	if got := resp.GetBuckets()[0]; got.GetWorkCaller().GetKind() != "team" || got.GetWorkCaller().GetId() != "team-1" || got.GetWorkKey().GetKind() != "board" || got.GetWorkKey().GetId() != "board-1" {
		t.Fatalf("bucket Work facts = %+v %+v", got.GetWorkCaller(), got.GetWorkKey())
	}
	if got := resp.GetBuckets()[0]; got.GetOwnerContext() != "case" || got.GetOwnerId() != "case-alpha" || got.GetUserId() != "user-1" {
		t.Fatalf("bucket attribution = %s:%q user:%q, want case:case-alpha/user-1", got.GetOwnerContext(), got.GetOwnerId(), got.GetUserId())
	}
	if got := resp.GetTotals()[0]; got.GetMetricKind() != "tool_call" || got.GetDimension() != "Read" || got.GetValue() != 2 {
		t.Fatalf("total = %+v", got)
	}
}

func TestGetWorkCallerTraceMetrics_UsesWorkCallerScope(t *testing.T) {
	var captured query.TraceMetricOptions
	app := newTestApplicationWithMetrics(fakeSpanQueryRepository{}, fakeTraceMetricRepository{
		getByWorkCallerFn: func(_ context.Context, opts query.TraceMetricOptions) (*query.TraceMetricResult, error) {
			captured = opts
			return &query.TraceMetricResult{Totals: []query.TraceMetricTotal{{MetricKind: "skill_call", Dimension: "designing-tests", Value: 1}}}, nil
		},
	})

	resp, err := app.GetWorkCallerTraceMetrics(authn.WithAuthenticatedUser(context.Background(), authn.AuthenticatedUser{ID: "test-reader"}),
		&tracepublicv1.GetWorkCallerTraceMetricsRequest{WorkCaller: &agentpublicv1.WorkCaller{Kind: "team", Id: "team-1"}, WorkKey: &agentpublicv1.WorkKey{Kind: "board", Id: "board-1"}, StepSeconds: 60})
	if err != nil {
		t.Fatalf("GetWorkCallerTraceMetrics: %v", err)
	}
	if captured.WorkCaller != teamCaller || captured.WorkKey != (query.WorkKey{Kind: "board", ID: "board-1"}) || captured.StepSeconds != 60 {
		t.Fatalf("captured options = %+v", captured)
	}
	if got := resp.GetTotals()[0]; got.GetMetricKind() != "skill_call" || got.GetDimension() != "designing-tests" || got.GetValue() != 1 {
		t.Fatalf("total = %+v", got)
	}
}

func TestGetSessionTraceMetrics_EmptyAgentWorkID_ReturnsInvalidArgument(t *testing.T) {
	app := newTestApplicationWithMetrics(fakeSpanQueryRepository{}, fakeTraceMetricRepository{
		getBySessionFn: func(context.Context, query.TraceMetricOptions) (*query.TraceMetricResult, error) {
			t.Fatal("GetBySession should not be called for empty agent_work_id")
			return nil, nil
		},
	})

	_, err := app.GetSessionTraceMetrics(authn.WithAuthenticatedUser(context.Background(), authn.AuthenticatedUser{ID: "test-reader"}),
		&tracepublicv1.GetSessionTraceMetricsRequest{WorkCaller: &agentpublicv1.WorkCaller{Kind: "team", Id: "team-1"}, AgentWorkId: "  "})
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("code = %s, want InvalidArgument (err=%v)", connect.CodeOf(err), err)
	}
}

func TestGetWorkCallerTraceMetrics_InvalidScope_ReturnsInvalidArgument(t *testing.T) {
	app := newTestApplicationWithMetrics(fakeSpanQueryRepository{}, fakeTraceMetricRepository{
		getByWorkCallerFn: func(context.Context, query.TraceMetricOptions) (*query.TraceMetricResult, error) {
			t.Fatal("GetByWorkCaller should not be called for an invalid scope")
			return nil, nil
		},
	})
	for name, req := range map[string]*tracepublicv1.GetWorkCallerTraceMetricsRequest{
		"no work caller":      {},
		"blank identity":      {WorkCaller: &agentpublicv1.WorkCaller{Kind: "team", Id: ""}},
		"malformed kind":      {WorkCaller: &agentpublicv1.WorkCaller{Kind: "Team", Id: "team-1"}},
		"person":              {WorkCaller: &agentpublicv1.WorkCaller{Kind: "person", Id: "user-1"}},
		"application":         {WorkCaller: &agentpublicv1.WorkCaller{Kind: "application", Id: "application-1"}},
		"incomplete work key": {WorkCaller: &agentpublicv1.WorkCaller{Kind: "team", Id: "team-1"}, WorkKey: &agentpublicv1.WorkKey{Kind: "board"}},
	} {
		_, err := app.GetWorkCallerTraceMetrics(authn.WithAuthenticatedUser(context.Background(), authn.AuthenticatedUser{ID: "test-reader"}), req)
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("%s: code = %s, want InvalidArgument (err=%v)", name, connect.CodeOf(err), err)
		}
	}
}

func TestGetSessionTraceMetrics_RequiresWorkReadPermission(t *testing.T) {
	authorizer := &fakeTraceAuthorizer{}
	app := newTestApplicationWithMetrics(fakeSpanQueryRepository{}, fakeTraceMetricRepository{
		getBySessionFn: func(context.Context, query.TraceMetricOptions) (*query.TraceMetricResult, error) {
			return &query.TraceMetricResult{}, nil
		},
	}, authorizer)

	ctx := authn.WithAuthenticatedUser(authn.WithAuthenticatedUser(context.Background(), authn.AuthenticatedUser{ID: "test-reader"}), authn.AuthenticatedUser{ID: "user-1"})
	_, err := app.GetSessionTraceMetrics(ctx, &tracepublicv1.GetSessionTraceMetricsRequest{WorkCaller: &agentpublicv1.WorkCaller{Kind: "team", Id: "team-1"}, AgentWorkId: "work-1"})
	if err != nil {
		t.Fatalf("GetSessionTraceMetrics: %v", err)
	}
	if authorizer.callerActor != "user-1" || authorizer.caller != teamCaller || authorizer.workID != "" {
		t.Fatalf("auth = %q for %+v, Work access for %q", authorizer.callerActor, authorizer.caller, authorizer.workID)
	}
}

func TestGetWorkCallerTraceMetrics_RequiresWorkCallerReadPermission(t *testing.T) {
	authorizer := &fakeTraceAuthorizer{}
	app := newTestApplicationWithMetrics(fakeSpanQueryRepository{}, fakeTraceMetricRepository{
		getByWorkCallerFn: func(context.Context, query.TraceMetricOptions) (*query.TraceMetricResult, error) {
			return &query.TraceMetricResult{}, nil
		},
	}, authorizer)

	ctx := authn.WithAuthenticatedUser(authn.WithAuthenticatedUser(context.Background(), authn.AuthenticatedUser{ID: "test-reader"}), authn.AuthenticatedUser{ID: "user-1"})
	_, err := app.GetWorkCallerTraceMetrics(ctx, &tracepublicv1.GetWorkCallerTraceMetricsRequest{WorkCaller: &agentpublicv1.WorkCaller{Kind: "team", Id: "team-1"}})
	if err != nil {
		t.Fatalf("GetWorkCallerTraceMetrics: %v", err)
	}
	if authorizer.callerActor != "user-1" || authorizer.caller != teamCaller || authorizer.workID != "" {
		t.Fatalf("auth = %q for %+v, Work access for %q", authorizer.callerActor, authorizer.caller, authorizer.workID)
	}
}

func TestGetSessionTraceMetrics_AuthDeniedDoesNotQueryMetrics(t *testing.T) {
	authorizer := &fakeTraceAuthorizer{callerErr: query.ErrAuthorizationPermissionDenied}
	app := newTestApplicationWithMetrics(fakeSpanQueryRepository{}, fakeTraceMetricRepository{
		getBySessionFn: func(context.Context, query.TraceMetricOptions) (*query.TraceMetricResult, error) {
			t.Fatal("metrics repository must not be queried when authorization fails")
			return nil, nil
		},
	}, authorizer)

	ctx := authn.WithAuthenticatedUser(authn.WithAuthenticatedUser(context.Background(), authn.AuthenticatedUser{ID: "test-reader"}), authn.AuthenticatedUser{ID: "user-1"})
	_, err := app.GetSessionTraceMetrics(ctx, &tracepublicv1.GetSessionTraceMetricsRequest{WorkCaller: &agentpublicv1.WorkCaller{Kind: "team", Id: "team-1"}, AgentWorkId: "work-1"})
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("code=%s, want PermissionDenied (err=%v)", connect.CodeOf(err), err)
	}
}
