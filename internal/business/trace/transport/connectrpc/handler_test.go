package connectrpc

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	tracepublicv1 "github.com/talgent-work/contracts/gen/trace/public/v1"
	"github.com/talgent-work/workbench/internal/business/trace/application"
	"github.com/talgent-work/workbench/internal/business/trace/application/query"

	"connectrpc.com/connect/v2"

	"github.com/talgent-work/workbench/internal/pkg/authn"
)

type fakeSpanQueryRepository struct {
	listBySessionFn func(context.Context, query.SpanListOptions) (*query.SpanListResult, error)
	listByOwnerFn   func(context.Context, query.SpanListOptions) (*query.SpanListResult, error)
	listFn          func(context.Context, string, int) ([]*query.Span, error)
	getFn           func(context.Context, query.SpanIdentity) (*query.Span, error)
}

type fakeTraceMetricRepository struct {
	getBySessionFn func(context.Context, query.TraceMetricOptions) (*query.TraceMetricResult, error)
	getByProjectFn func(context.Context, query.TraceMetricOptions) (*query.TraceMetricResult, error)
	getByPersonFn  func(context.Context, string, query.TraceMetricOptions) (*query.TraceMetricResult, error)
}

// fakeTraceAuthorizer stands for both the Project access decision and
// Agent's Work access, recording whom each was asked about.
type fakeTraceAuthorizer struct {
	projectActor string
	projectID    string
	projectErr   error
	workActor    string
	workID       string
	workErr      error
}

func (f *fakeTraceAuthorizer) AuthorizeProjectReader(_ context.Context, readerID, projectID string) error {
	f.projectActor, f.projectID = readerID, projectID
	return f.projectErr
}

func (f *fakeTraceAuthorizer) AuthorizeWorkReader(_ context.Context, readerID, workID string) error {
	f.workActor, f.workID = readerID, workID
	return f.workErr
}

type fakeEventRepository struct {
	listFn func(context.Context, query.EventOptions) (*query.EventResult, error)
}

func (f fakeEventRepository) ListEvents(ctx context.Context, opts query.EventOptions) (*query.EventResult, error) {
	if f.listFn != nil {
		return f.listFn(ctx, opts)
	}
	return &query.EventResult{}, nil
}

func (f fakeSpanQueryRepository) ListBySession(ctx context.Context, opts query.SpanListOptions) (*query.SpanListResult, error) {
	if f.listBySessionFn != nil {
		return f.listBySessionFn(ctx, opts)
	}
	if f.listFn != nil {
		spans, err := f.listFn(ctx, opts.AgentWorkID, opts.Limit)
		return &query.SpanListResult{Spans: spans}, err
	}
	return nil, nil
}

func (f fakeSpanQueryRepository) ListByOwner(ctx context.Context, opts query.SpanListOptions) (*query.SpanListResult, error) {
	if f.listByOwnerFn != nil {
		return f.listByOwnerFn(ctx, opts)
	}
	return nil, nil
}

func (f fakeSpanQueryRepository) GetByIdentity(ctx context.Context, identity query.SpanIdentity) (*query.Span, error) {
	if f.getFn != nil {
		return f.getFn(ctx, identity)
	}
	return nil, nil
}

func (f fakeTraceMetricRepository) GetBySession(ctx context.Context, opts query.TraceMetricOptions) (*query.TraceMetricResult, error) {
	if f.getBySessionFn != nil {
		return f.getBySessionFn(ctx, opts)
	}
	return nil, nil
}

func (f fakeTraceMetricRepository) GetByProject(ctx context.Context, opts query.TraceMetricOptions) (*query.TraceMetricResult, error) {
	if f.getByProjectFn != nil {
		return f.getByProjectFn(ctx, opts)
	}
	return nil, nil
}

func (f fakeTraceMetricRepository) GetByPerson(ctx context.Context, personID string, opts query.TraceMetricOptions) (*query.TraceMetricResult, error) {
	if f.getByPersonFn != nil {
		return f.getByPersonFn(ctx, personID, opts)
	}
	return nil, nil
}

// Intent: a valid list request should return repository spans as the public
// proto response rather than the old Unimplemented stub.
func TestListSpansBySession_ReturnsSpans(t *testing.T) {
	start := time.Date(2026, 4, 29, 10, 0, 0, 0, time.UTC)
	repo := fakeSpanQueryRepository{
		listFn: func(_ context.Context, sessionID string, limit int) ([]*query.Span, error) {
			if sessionID != "work-1" {
				t.Fatalf("sessionID = %q, want work-1", sessionID)
			}
			if limit != 25 {
				t.Fatalf("limit = %d, want 25", limit)
			}
			return []*query.Span{sampleSpan(start)}, nil
		},
	}
	app := newTestApplication(repo)

	resp, err := app.ListSpansBySession(authn.WithAuthenticatedUser(context.Background(), authn.AuthenticatedUser{ID: "test-reader"}),
		&tracepublicv1.ListSpansBySessionRequest{ProjectId: "project-1", AgentWorkId: "work-1", Limit: 25})
	if err != nil {
		t.Fatalf("ListSpansBySession: %v", err)
	}
	if len(resp.GetSpans()) != 1 {
		t.Fatalf("spans len = %d, want 1", len(resp.GetSpans()))
	}
	got := resp.GetSpans()[0]
	if got.GetSpanId() != "span-1" || got.GetTraceId() != "trace-1" || got.GetAgentWorkId() != "work-1" || got.GetName() != "turn" {
		t.Fatalf("unexpected span: %+v", got)
	}
	if got.GetAttributes()["talgent.agent_work_id"] != "work-1" {
		t.Fatalf("attributes = %+v, want talgent.agent_work_id", got.GetAttributes())
	}
	if got.GetStartTime().AsTime() != start {
		t.Fatalf("start_time = %s, want %s", got.GetStartTime().AsTime(), start)
	}
}

// Intent: malformed list requests should fail at the protocol boundary instead
// of issuing a broad ClickHouse query.
func TestListSpansBySession_EmptySessionID_ReturnsInvalidArgument(t *testing.T) {
	app := newTestApplication(fakeSpanQueryRepository{})

	_, err := app.ListSpansBySession(authn.WithAuthenticatedUser(context.Background(), authn.AuthenticatedUser{ID: "test-reader"}),
		&tracepublicv1.ListSpansBySessionRequest{ProjectId: "project-1", AgentWorkId: "  "})
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("code = %s, want InvalidArgument (err=%v)", connect.CodeOf(err), err)
	}
}

// Intent: repository failures should surface as Internal so Portal can show a
// query error instead of treating infrastructure failure as an empty trace.
func TestListSpansBySession_RepositoryError_ReturnsInternal(t *testing.T) {
	app := newTestApplication(fakeSpanQueryRepository{
		listFn: func(context.Context, string, int) ([]*query.Span, error) {
			return nil, errors.New("clickhouse down")
		},
	})

	_, err := app.ListSpansBySession(authn.WithAuthenticatedUser(context.Background(), authn.AuthenticatedUser{ID: "test-reader"}),
		&tracepublicv1.ListSpansBySessionRequest{ProjectId: "project-1", AgentWorkId: "work-1"})
	if connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("code = %s, want Internal (err=%v)", connect.CodeOf(err), err)
	}
}

// Intent: a valid GetSpan request should return the requested span as the
// public proto response rather than the old Unimplemented stub.
func TestGetSpan_ReturnsSpan(t *testing.T) {
	start := time.Date(2026, 4, 29, 10, 0, 0, 0, time.UTC)
	app := newTestApplication(fakeSpanQueryRepository{
		getFn: func(_ context.Context, identity query.SpanIdentity) (*query.Span, error) {
			if identity != (query.SpanIdentity{ProjectID: "project-1", TraceID: "trace-1", SpanID: "span-1"}) {
				t.Fatalf("span identity = %+v", identity)
			}
			return sampleSpan(start), nil
		},
	})

	resp, err := app.GetSpan(authn.WithAuthenticatedUser(context.Background(), authn.AuthenticatedUser{ID: "test-reader"}),
		&tracepublicv1.GetSpanRequest{ProjectId: "project-1", TraceId: "trace-1", SpanId: "span-1"})
	if err != nil {
		t.Fatalf("GetSpan: %v", err)
	}
	if resp.GetSpan().GetSpanId() != "span-1" {
		t.Fatalf("span_id = %q, want span-1", resp.GetSpan().GetSpanId())
	}
}

// Intent: a missing span should map to NotFound, not a nil response or Internal.
func TestGetSpan_Missing_ReturnsNotFound(t *testing.T) {
	app := newTestApplication(fakeSpanQueryRepository{
		getFn: func(context.Context, query.SpanIdentity) (*query.Span, error) {
			return nil, nil
		},
	})

	_, err := app.GetSpan(authn.WithAuthenticatedUser(context.Background(), authn.AuthenticatedUser{ID: "test-reader"}),
		&tracepublicv1.GetSpanRequest{ProjectId: "project-1", TraceId: "trace-1", SpanId: "missing"})
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("code = %s, want NotFound (err=%v)", connect.CodeOf(err), err)
	}
}

// Intent: malformed get requests should fail before reaching repository lookup.
func TestGetSpan_EmptySpanID_ReturnsInvalidArgument(t *testing.T) {
	app := newTestApplication(fakeSpanQueryRepository{})

	_, err := app.GetSpan(authn.WithAuthenticatedUser(context.Background(), authn.AuthenticatedUser{ID: "test-reader"}),
		&tracepublicv1.GetSpanRequest{ProjectId: "project-1", TraceId: "trace-1", SpanId: "  "})
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("code = %s, want InvalidArgument (err=%v)", connect.CodeOf(err), err)
	}
}

// Intent: Work-scoped trace queries expose native call identity in
// the public response so Portal can join spans with stream activities.
func TestListSpansBySession_ReturnsNativeAssociationAndCursor(t *testing.T) {
	start := time.Date(2026, 4, 29, 10, 0, 0, 0, time.UTC)
	repo := fakeSpanQueryRepository{
		listBySessionFn: func(_ context.Context, opts query.SpanListOptions) (*query.SpanListResult, error) {
			if opts.AgentWorkID != "work-1" {
				t.Fatalf("AgentWorkID = %q, want work-1", opts.AgentWorkID)
			}
			if opts.Limit != 25 {
				t.Fatalf("Limit = %d, want 25", opts.Limit)
			}
			if opts.AfterCursor != "cursor-1" {
				t.Fatalf("AfterCursor = %q, want cursor-1", opts.AfterCursor)
			}
			return &query.SpanListResult{
				Spans:      []*query.Span{sampleWorkSpan(start)},
				NextCursor: "cursor-2",
			}, nil
		},
	}
	app := newTestApplication(repo)

	resp, err := app.ListSpansBySession(authn.WithAuthenticatedUser(context.Background(), authn.AuthenticatedUser{ID: "test-reader"}),
		&tracepublicv1.ListSpansBySessionRequest{ProjectId: "project-1",
			AgentWorkId: "work-1",
			Limit:       25,
			AfterCursor: "cursor-1",
		})
	if err != nil {
		t.Fatalf("ListSpansBySession: %v", err)
	}
	if resp.GetNextCursor() != "cursor-2" {
		t.Fatalf("next_cursor = %q, want cursor-2", resp.GetNextCursor())
	}
	if len(resp.GetSpans()) != 1 {
		t.Fatalf("spans len = %d, want 1", len(resp.GetSpans()))
	}
	got := resp.GetSpans()[0]
	if got.GetAgentWorkId() != "work-1" {
		t.Fatalf("agent_work_id = %q, want work-1", got.GetAgentWorkId())
	}
	if got.GetCallId() != "call-1" {
		t.Fatalf("call_id = %q, want op-1", got.GetCallId())
	}
}

// Intent: malformed Work queries should fail before repository access to
// prevent accidental broad trace scans.
func TestListSpansBySession_EmptyAgentWorkID_ReturnsInvalidArgument(t *testing.T) {
	app := newTestApplication(fakeSpanQueryRepository{
		listBySessionFn: func(context.Context, query.SpanListOptions) (*query.SpanListResult, error) {
			t.Fatal("ListBySession should not be called for empty agent_work_id")
			return nil, nil
		},
	})

	_, err := app.ListSpansBySession(authn.WithAuthenticatedUser(context.Background(), authn.AuthenticatedUser{ID: "test-reader"}),
		&tracepublicv1.ListSpansBySessionRequest{ProjectId: "project-1", AgentWorkId: "  "})
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("code = %s, want InvalidArgument (err=%v)", connect.CodeOf(err), err)
	}
}

// Intent: bad cursors are client input errors, not infrastructure failures.
func TestListSpansBySession_InvalidCursor_ReturnsInvalidArgument(t *testing.T) {
	app := newTestApplication(fakeSpanQueryRepository{
		listBySessionFn: func(context.Context, query.SpanListOptions) (*query.SpanListResult, error) {
			return nil, query.ErrInvalidSpanCursor
		},
	})

	_, err := app.ListSpansBySession(authn.WithAuthenticatedUser(context.Background(), authn.AuthenticatedUser{ID: "test-reader"}),
		&tracepublicv1.ListSpansBySessionRequest{ProjectId: "project-1",
			AgentWorkId: "work-1",
			AfterCursor: "bad-cursor",
		})
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("code = %s, want InvalidArgument (err=%v)", connect.CodeOf(err), err)
	}
}

// Intent: owner-scoped trace queries must require project_id plus owner
// context/id, authorize by project, and forward optional runtime_purpose.
func TestListSpansByOwner_RequiresProjectReadPermission(t *testing.T) {
	authorizer := &fakeTraceAuthorizer{}
	var captured query.SpanListOptions
	app := newTestApplication(fakeSpanQueryRepository{
		listByOwnerFn: func(_ context.Context, opts query.SpanListOptions) (*query.SpanListResult, error) {
			captured = opts
			return &query.SpanListResult{Spans: []*query.Span{sampleOwnerSpan(time.Now())}}, nil
		},
	}, authorizer)

	ctx := authn.WithAuthenticatedUser(authn.WithAuthenticatedUser(context.Background(), authn.AuthenticatedUser{ID: "test-reader"}), authn.AuthenticatedUser{ID: "user-1"})
	_, err := app.ListSpansByOwner(ctx,
		&tracepublicv1.ListSpansByOwnerRequest{
			ProjectId:      "project-1",
			OwnerContext:   "caller",
			OwnerId:        "project-1",
			RuntimePurpose: "maintainer",
		})
	if err != nil {
		t.Fatalf("ListSpansByOwner: %v", err)
	}
	if authorizer.projectID != "project-1" {
		t.Fatalf("project auth = project:%q", authorizer.projectID)
	}
	if captured.ProjectID != "project-1" || captured.OwnerContext != "caller" || captured.OwnerID != "project-1" || captured.RuntimePurpose != "maintainer" {
		t.Fatalf("captured options = %+v", captured)
	}
}

// Intent: owner list requests missing a required scope component should fail
// before repository access to avoid project-wide unqualified owner scans.
func TestListSpansByOwner_EmptyRequiredFields_ReturnsInvalidArgument(t *testing.T) {
	app := newTestApplication(fakeSpanQueryRepository{
		listByOwnerFn: func(context.Context, query.SpanListOptions) (*query.SpanListResult, error) {
			t.Fatal("ListByOwner should not be called for invalid requests")
			return nil, nil
		},
	})

	for _, tc := range []struct {
		name string
		req  *tracepublicv1.ListSpansByOwnerRequest
	}{
		{name: "project", req: &tracepublicv1.ListSpansByOwnerRequest{OwnerContext: "caller", OwnerId: "project-1"}},
		{name: "owner context", req: &tracepublicv1.ListSpansByOwnerRequest{ProjectId: "project-1", OwnerId: "project-1"}},
		{name: "owner id", req: &tracepublicv1.ListSpansByOwnerRequest{ProjectId: "project-1", OwnerContext: "caller"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := app.ListSpansByOwner(authn.WithAuthenticatedUser(context.Background(), authn.AuthenticatedUser{ID: "test-reader"}), tc.req)
			if connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatalf("code = %s, want InvalidArgument (err=%v)", connect.CodeOf(err), err)
			}
		})
	}
}

// Intent: Work metrics must use the metric-bucket query path, not the raw span
// listing path, so overview charts do not trigger dashboard-time span scans.
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
						ProjectID:     "project-1",
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
		&tracepublicv1.GetSessionTraceMetricsRequest{ProjectId: "project-1",
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
	if got := resp.GetBuckets()[0]; got.GetOwnerContext() != "case" || got.GetOwnerId() != "case-alpha" || got.GetUserId() != "user-1" {
		t.Fatalf("bucket attribution = %s:%q user:%q, want case:case-alpha/user-1", got.GetOwnerContext(), got.GetOwnerId(), got.GetUserId())
	}
	if got := resp.GetTotals()[0]; got.GetMetricKind() != "tool_call" || got.GetDimension() != "Read" || got.GetValue() != 2 {
		t.Fatalf("total = %+v", got)
	}
}

// Intent: Project metrics must use project scope directly so Trace never calls
// a caller's APIs or enumerates Works to build dashboard charts.
func TestGetProjectTraceMetrics_UsesProjectScope(t *testing.T) {
	var captured query.TraceMetricOptions
	app := newTestApplicationWithMetrics(fakeSpanQueryRepository{}, fakeTraceMetricRepository{
		getByProjectFn: func(_ context.Context, opts query.TraceMetricOptions) (*query.TraceMetricResult, error) {
			captured = opts
			return &query.TraceMetricResult{Totals: []query.TraceMetricTotal{{MetricKind: "skill_call", Dimension: "designing-tests", Value: 1}}}, nil
		},
	})

	resp, err := app.GetProjectTraceMetrics(authn.WithAuthenticatedUser(context.Background(), authn.AuthenticatedUser{ID: "test-reader"}),
		&tracepublicv1.GetProjectTraceMetricsRequest{ProjectId: " project-1 ", StepSeconds: 60})
	if err != nil {
		t.Fatalf("GetProjectTraceMetrics: %v", err)
	}
	if captured.ProjectID != "project-1" || captured.StepSeconds != 60 {
		t.Fatalf("captured options = %+v", captured)
	}
	if got := resp.GetTotals()[0]; got.GetMetricKind() != "skill_call" || got.GetDimension() != "designing-tests" || got.GetValue() != 1 {
		t.Fatalf("total = %+v", got)
	}
}

// Intent: malformed metrics requests must fail before repository access to
// prevent accidental unscoped metric scans.
func TestGetSessionTraceMetrics_EmptyAgentWorkID_ReturnsInvalidArgument(t *testing.T) {
	app := newTestApplicationWithMetrics(fakeSpanQueryRepository{}, fakeTraceMetricRepository{
		getBySessionFn: func(context.Context, query.TraceMetricOptions) (*query.TraceMetricResult, error) {
			t.Fatal("GetBySession should not be called for empty agent_work_id")
			return nil, nil
		},
	})

	_, err := app.GetSessionTraceMetrics(authn.WithAuthenticatedUser(context.Background(), authn.AuthenticatedUser{ID: "test-reader"}),
		&tracepublicv1.GetSessionTraceMetricsRequest{ProjectId: "project-1", AgentWorkId: "  "})
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("code = %s, want InvalidArgument (err=%v)", connect.CodeOf(err), err)
	}
}

// Intent: malformed project metrics requests must fail before repository
// access to prevent broad project-dashboard scans.
func TestGetProjectTraceMetrics_EmptyProjectID_ReturnsInvalidArgument(t *testing.T) {
	app := newTestApplicationWithMetrics(fakeSpanQueryRepository{}, fakeTraceMetricRepository{
		getByProjectFn: func(context.Context, query.TraceMetricOptions) (*query.TraceMetricResult, error) {
			t.Fatal("GetByProject should not be called for empty project_id")
			return nil, nil
		},
	})

	_, err := app.GetProjectTraceMetrics(authn.WithAuthenticatedUser(context.Background(), authn.AuthenticatedUser{ID: "test-reader"}),
		&tracepublicv1.GetProjectTraceMetricsRequest{ProjectId: "  "})
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("code = %s, want InvalidArgument (err=%v)", connect.CodeOf(err), err)
	}
}

// Intent: Work metrics are scoped by Work authorization, not by a caller's
// own detail reconstruction or raw span enumeration.
func TestGetSessionTraceMetrics_RequiresWorkReadPermission(t *testing.T) {
	authorizer := &fakeTraceAuthorizer{}
	app := newTestApplicationWithMetrics(fakeSpanQueryRepository{}, fakeTraceMetricRepository{
		getBySessionFn: func(context.Context, query.TraceMetricOptions) (*query.TraceMetricResult, error) {
			return &query.TraceMetricResult{}, nil
		},
	}, authorizer)

	ctx := authn.WithAuthenticatedUser(authn.WithAuthenticatedUser(context.Background(), authn.AuthenticatedUser{ID: "test-reader"}), authn.AuthenticatedUser{ID: "user-1"})
	_, err := app.GetSessionTraceMetrics(ctx, &tracepublicv1.GetSessionTraceMetricsRequest{ProjectId: "project-1", AgentWorkId: "work-1"})
	if err != nil {
		t.Fatalf("GetSessionTraceMetrics: %v", err)
	}
	if authorizer.projectActor != "user-1" || authorizer.projectID != "project-1" {
		t.Fatalf("work auth = actor:%q work:%q", authorizer.projectActor, authorizer.projectID)
	}
}

// Intent: Project metrics are scoped by Project authorization directly; Trace
// must not use a caller's APIs as a proxy for Project context.
func TestGetProjectTraceMetrics_RequiresProjectReadPermission(t *testing.T) {
	authorizer := &fakeTraceAuthorizer{}
	app := newTestApplicationWithMetrics(fakeSpanQueryRepository{}, fakeTraceMetricRepository{
		getByProjectFn: func(context.Context, query.TraceMetricOptions) (*query.TraceMetricResult, error) {
			return &query.TraceMetricResult{}, nil
		},
	}, authorizer)

	ctx := authn.WithAuthenticatedUser(authn.WithAuthenticatedUser(context.Background(), authn.AuthenticatedUser{ID: "test-reader"}), authn.AuthenticatedUser{ID: "user-1"})
	_, err := app.GetProjectTraceMetrics(ctx, &tracepublicv1.GetProjectTraceMetricsRequest{ProjectId: "project-1"})
	if err != nil {
		t.Fatalf("GetProjectTraceMetrics: %v", err)
	}
	if authorizer.projectActor != "user-1" || authorizer.projectID != "project-1" {
		t.Fatalf("project auth = actor:%q project:%q", authorizer.projectActor, authorizer.projectID)
	}
}

func TestGetSessionTraceMetrics_AuthDeniedDoesNotQueryMetrics(t *testing.T) {
	authorizer := &fakeTraceAuthorizer{projectErr: query.ErrAuthorizationPermissionDenied}
	app := newTestApplicationWithMetrics(fakeSpanQueryRepository{}, fakeTraceMetricRepository{
		getBySessionFn: func(context.Context, query.TraceMetricOptions) (*query.TraceMetricResult, error) {
			t.Fatal("metrics repository must not be queried when authorization fails")
			return nil, nil
		},
	}, authorizer)

	ctx := authn.WithAuthenticatedUser(authn.WithAuthenticatedUser(context.Background(), authn.AuthenticatedUser{ID: "test-reader"}), authn.AuthenticatedUser{ID: "user-1"})
	_, err := app.GetSessionTraceMetrics(ctx, &tracepublicv1.GetSessionTraceMetricsRequest{ProjectId: "project-1", AgentWorkId: "work-1"})
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("code=%s, want PermissionDenied (err=%v)", connect.CodeOf(err), err)
	}
}

func TestListSpansBySession_RequiresWorkReadPermission(t *testing.T) {
	authorizer := &fakeTraceAuthorizer{}
	app := newTestApplication(fakeSpanQueryRepository{
		listBySessionFn: func(context.Context, query.SpanListOptions) (*query.SpanListResult, error) {
			return &query.SpanListResult{}, nil
		},
	}, authorizer)

	ctx := authn.WithAuthenticatedUser(authn.WithAuthenticatedUser(context.Background(), authn.AuthenticatedUser{ID: "test-reader"}), authn.AuthenticatedUser{ID: "user-1"})
	_, err := app.ListSpansBySession(ctx, &tracepublicv1.ListSpansBySessionRequest{ProjectId: "project-1", AgentWorkId: "work-1"})
	if err != nil {
		t.Fatalf("ListSpansBySession: %v", err)
	}
	if authorizer.projectActor != "user-1" || authorizer.projectID != "project-1" {
		t.Fatalf("work auth = actor:%q work:%q", authorizer.projectActor, authorizer.projectID)
	}
}

func TestGetSpan_RequiresWorkReadPermissionForFetchedSpan(t *testing.T) {
	authorizer := &fakeTraceAuthorizer{}
	app := newTestApplication(fakeSpanQueryRepository{
		getFn: func(context.Context, query.SpanIdentity) (*query.Span, error) {
			return &query.Span{TraceID: "trace-1", SpanID: "span-1", ProjectID: "project-1", AgentWorkID: "work-1"}, nil
		},
	}, authorizer)

	ctx := authn.WithAuthenticatedUser(authn.WithAuthenticatedUser(context.Background(), authn.AuthenticatedUser{ID: "test-reader"}), authn.AuthenticatedUser{ID: "user-1"})
	_, err := app.GetSpan(ctx, &tracepublicv1.GetSpanRequest{ProjectId: "project-1", TraceId: "trace-1", SpanId: "span-1"})
	if err != nil {
		t.Fatalf("GetSpan: %v", err)
	}
	if authorizer.projectActor != "user-1" || authorizer.projectID != "project-1" {
		t.Fatalf("work auth = actor:%q work:%q", authorizer.projectActor, authorizer.projectID)
	}
}

// Intent: non-Work spans with project_id should authorize against project
// permission instead of being denied only because agent_work_id is empty.
func TestGetSpan_RequiresProjectReadPermissionForNonWorkSpan(t *testing.T) {
	authorizer := &fakeTraceAuthorizer{}
	app := newTestApplication(fakeSpanQueryRepository{
		getFn: func(context.Context, query.SpanIdentity) (*query.Span, error) {
			return &query.Span{
				SpanID:         "span-1",
				TraceID:        "trace-1",
				ProjectID:      "project-1",
				RuntimePurpose: "maintainer",
				OwnerContext:   "caller",
				OwnerID:        "project-1",
			}, nil
		},
	}, authorizer)

	ctx := authn.WithAuthenticatedUser(authn.WithAuthenticatedUser(context.Background(), authn.AuthenticatedUser{ID: "test-reader"}), authn.AuthenticatedUser{ID: "user-1"})
	_, err := app.GetSpan(ctx, &tracepublicv1.GetSpanRequest{ProjectId: "project-1", TraceId: "trace-1", SpanId: "span-1"})
	if err != nil {
		t.Fatalf("GetSpan: %v", err)
	}
	if authorizer.projectActor != "user-1" || authorizer.projectID != "project-1" {
		t.Fatalf("project auth = actor:%q project:%q", authorizer.projectActor, authorizer.projectID)
	}
}

// Intent: non-Work spans without project_id should not be exposed because
// Trace cannot prove caller scope for owner-neutral data.
func TestGetSpan_NonWorkSpanWithoutProjectID_ReturnsPermissionDenied(t *testing.T) {
	app := newTestApplication(fakeSpanQueryRepository{
		getFn: func(context.Context, query.SpanIdentity) (*query.Span, error) {
			return &query.Span{TraceID: "trace-1", SpanID: "span-1", RuntimePurpose: "maintainer"}, nil
		},
	}, &fakeTraceAuthorizer{})

	ctx := authn.WithAuthenticatedUser(authn.WithAuthenticatedUser(context.Background(), authn.AuthenticatedUser{ID: "test-reader"}), authn.AuthenticatedUser{ID: "user-1"})
	_, err := app.GetSpan(ctx, &tracepublicv1.GetSpanRequest{ProjectId: "project-1", TraceId: "trace-1", SpanId: "span-1"})
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("code=%s, want PermissionDenied (err=%v)", connect.CodeOf(err), err)
	}
}

func newTestApplication(repo query.SpanQueryRepository, authorities ...*fakeTraceAuthorizer) *Handler {
	return newTestApplicationWithMetrics(repo, fakeTraceMetricRepository{}, authorities...)
}

// newTestApplicationWithMetrics admits every reader unless given an authority;
// an explicit nil authority leaves both decisions unavailable.
func newTestApplicationWithMetrics(repo query.SpanQueryRepository, metrics query.TraceMetricRepository, authorities ...*fakeTraceAuthorizer) *Handler {
	return newTestApplicationWithEvents(repo, metrics, fakeEventRepository{}, authorities...)
}

func newTestApplicationWithEvents(repo query.SpanQueryRepository, metrics query.TraceMetricRepository, events query.EventRepository, authorities ...*fakeTraceAuthorizer) *Handler {
	authority := &fakeTraceAuthorizer{}
	if len(authorities) > 0 {
		authority = authorities[0]
	}
	if authority == nil {
		return NewHandler(application.NewApplication(query.NewQueries(repo, metrics, events, nil, nil)))
	}
	return NewHandler(application.NewApplication(query.NewQueries(repo, metrics, events, authority, authority)))
}

func sampleSpan(start time.Time) *query.Span {
	return &query.Span{
		ProjectID:    "project-1",
		AgentWorkID:  "work-1",
		SpanID:       "span-1",
		TraceID:      "trace-1",
		ParentSpanID: "",

		Name:          "turn",
		Kind:          "Internal",
		StartTime:     start,
		EndTime:       start.Add(1500 * time.Millisecond),
		DurationNanos: int64(1500 * time.Millisecond),
		StatusCode:    "Ok",
		StatusMessage: "",
		Attributes: map[string]string{
			"talgent.agent_work_id": "work-1",
			"talgent.closed_by":     "result",
		},
	}
}

func sampleWorkSpan(start time.Time) *query.Span {
	sp := sampleSpan(start)
	sp.AgentWorkID = "work-1"
	sp.CallID = "call-1"
	return sp
}

func sampleOwnerSpan(start time.Time) *query.Span {
	return &query.Span{
		SpanID:         "span-1",
		TraceID:        "trace-1",
		ProjectID:      "project-1",
		AgentID:        "runtime-1",
		RuntimePurpose: "maintainer",
		OwnerContext:   "caller",
		OwnerID:        "project-1",
		Name:           "tool_call:Read",
		Kind:           "Internal",
		StartTime:      start,
		EndTime:        start.Add(time.Second),
		DurationNanos:  int64(time.Second),
		StatusCode:     "Ok",
		Attributes: map[string]string{
			"talgent.project_id":      "project-1",
			"talgent.agent_id":        "runtime-1",
			"talgent.runtime_purpose": "maintainer",
			"talgent.owner_context":   "caller",
			"talgent.owner_id":        "project-1",
		},
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestMissingAuthorityCannotReadSpans(t *testing.T) {
	app := newTestApplication(fakeSpanQueryRepository{listBySessionFn: func(context.Context, query.SpanListOptions) (*query.SpanListResult, error) {
		t.Fatal("must not query spans without authority")
		return nil, nil
	}}, nil)
	_, err := app.ListSpansBySession(authn.WithAuthenticatedUser(context.Background(), authn.AuthenticatedUser{ID: "reader"}), &tracepublicv1.ListSpansBySessionRequest{ProjectId: "project-1", AgentWorkId: "private"})
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("missing authority: %v", err)
	}
}
func TestMissingIdentityCannotReadSpans(t *testing.T) {
	app := newTestApplication(fakeSpanQueryRepository{listBySessionFn: func(context.Context, query.SpanListOptions) (*query.SpanListResult, error) {
		t.Fatal("must not query spans without identity")
		return nil, nil
	}})
	_, err := app.ListSpansBySession(context.Background(), &tracepublicv1.ListSpansBySessionRequest{ProjectId: "project-1", AgentWorkId: "private"})
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("missing identity: %v", err)
	}
}
