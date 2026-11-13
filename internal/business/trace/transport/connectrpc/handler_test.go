package connectrpc

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	agentpublicv1 "github.com/talgent-work/contracts/gen/go/agent/public/v1"
	tracepublicv1 "github.com/talgent-work/contracts/gen/go/trace/public/v1"
	"github.com/talgent-work/workbench/internal/business/trace/application/query"

	"connectrpc.com/connect/v2"

	"github.com/talgent-work/workbench/internal/pkg/authn"
)

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
		&tracepublicv1.ListSpansBySessionRequest{WorkCaller: &agentpublicv1.WorkCaller{Kind: "team", Id: "team-1"}, AgentWorkId: "work-1", Limit: 25})
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
		&tracepublicv1.ListSpansBySessionRequest{WorkCaller: &agentpublicv1.WorkCaller{Kind: "team", Id: "team-1"}, AgentWorkId: "  "})
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
		&tracepublicv1.ListSpansBySessionRequest{WorkCaller: &agentpublicv1.WorkCaller{Kind: "team", Id: "team-1"}, AgentWorkId: "work-1"})
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
			if identity != (query.SpanIdentity{WorkCaller: teamCaller, TraceID: "trace-1", SpanID: "span-1"}) {
				t.Fatalf("span identity = %+v", identity)
			}
			return sampleSpan(start), nil
		},
	})

	resp, err := app.GetSpan(authn.WithAuthenticatedUser(context.Background(), authn.AuthenticatedUser{ID: "test-reader"}),
		&tracepublicv1.GetSpanRequest{WorkCaller: &agentpublicv1.WorkCaller{Kind: "team", Id: "team-1"}, TraceId: "trace-1", SpanId: "span-1"})
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
		&tracepublicv1.GetSpanRequest{WorkCaller: &agentpublicv1.WorkCaller{Kind: "team", Id: "team-1"}, TraceId: "trace-1", SpanId: "missing"})
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("code = %s, want NotFound (err=%v)", connect.CodeOf(err), err)
	}
}

// Intent: malformed get requests should fail before reaching repository lookup.
func TestGetSpan_EmptySpanID_ReturnsInvalidArgument(t *testing.T) {
	app := newTestApplication(fakeSpanQueryRepository{})

	_, err := app.GetSpan(authn.WithAuthenticatedUser(context.Background(), authn.AuthenticatedUser{ID: "test-reader"}),
		&tracepublicv1.GetSpanRequest{WorkCaller: &agentpublicv1.WorkCaller{Kind: "team", Id: "team-1"}, TraceId: "trace-1", SpanId: "  "})
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
		&tracepublicv1.ListSpansBySessionRequest{WorkCaller: &agentpublicv1.WorkCaller{Kind: "team", Id: "team-1"},
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
		&tracepublicv1.ListSpansBySessionRequest{WorkCaller: &agentpublicv1.WorkCaller{Kind: "team", Id: "team-1"}, AgentWorkId: "  "})
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
		&tracepublicv1.ListSpansBySessionRequest{WorkCaller: &agentpublicv1.WorkCaller{Kind: "team", Id: "team-1"},
			AgentWorkId: "work-1",
			AfterCursor: "bad-cursor",
		})
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("code = %s, want InvalidArgument (err=%v)", connect.CodeOf(err), err)
	}
}

// Intent: owner-scoped trace queries must require a Work Caller plus owner
// context/id, authorize by the Work Caller's extension, and forward optional
// runtime_purpose.
func TestListSpansByOwner_RequiresWorkCallerReadPermission(t *testing.T) {
	authorizer := &fakeTraceAuthorizer{}
	var captured query.SpanListOptions
	app := newTestApplication(fakeSpanQueryRepository{
		listByWorkCallerFn: func(_ context.Context, opts query.SpanListOptions) (*query.SpanListResult, error) {
			captured = opts
			return &query.SpanListResult{Spans: []*query.Span{sampleOwnerSpan(time.Now())}}, nil
		},
	}, authorizer)

	ctx := authn.WithAuthenticatedUser(authn.WithAuthenticatedUser(context.Background(), authn.AuthenticatedUser{ID: "test-reader"}), authn.AuthenticatedUser{ID: "user-1"})
	_, err := app.ListSpansByOwner(ctx,
		&tracepublicv1.ListSpansByOwnerRequest{
			WorkCaller:     &agentpublicv1.WorkCaller{Kind: "team", Id: "team-1"},
			OwnerContext:   "caller",
			OwnerId:        "board-1",
			RuntimePurpose: "maintainer",
		})
	if err != nil {
		t.Fatalf("ListSpansByOwner: %v", err)
	}
	if authorizer.caller != teamCaller || authorizer.callerActor != "user-1" {
		t.Fatalf("work caller auth = %q for %+v", authorizer.callerActor, authorizer.caller)
	}
	if captured.WorkCaller != teamCaller || captured.OwnerContext != "caller" || captured.OwnerID != "board-1" || captured.RuntimePurpose != "maintainer" {
		t.Fatalf("captured options = %+v", captured)
	}
}

// Intent: owner list requests missing a required scope component should fail
// before repository access to avoid Work Caller-wide unqualified owner scans.
func TestListSpansByOwner_EmptyRequiredFields_ReturnsInvalidArgument(t *testing.T) {
	app := newTestApplication(fakeSpanQueryRepository{
		listByWorkCallerFn: func(context.Context, query.SpanListOptions) (*query.SpanListResult, error) {
			t.Fatal("ListByWorkCaller should not be called for invalid requests")
			return nil, nil
		},
	})

	for _, tc := range []struct {
		name string
		req  *tracepublicv1.ListSpansByOwnerRequest
	}{
		{name: "work caller", req: &tracepublicv1.ListSpansByOwnerRequest{OwnerContext: "caller", OwnerId: "board-1"}},
		{name: "person work caller", req: &tracepublicv1.ListSpansByOwnerRequest{WorkCaller: &agentpublicv1.WorkCaller{Kind: "person", Id: "user-1"}, OwnerContext: "caller", OwnerId: "board-1"}},
		{name: "owner context", req: &tracepublicv1.ListSpansByOwnerRequest{WorkCaller: &agentpublicv1.WorkCaller{Kind: "team", Id: "team-1"}, OwnerId: "board-1"}},
		{name: "owner id", req: &tracepublicv1.ListSpansByOwnerRequest{WorkCaller: &agentpublicv1.WorkCaller{Kind: "team", Id: "team-1"}, OwnerContext: "caller"}},
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
// Intent: Work Caller metrics read the Work Caller's scope directly, narrowed
// to one Work Key, so Trace never enumerates Works to build dashboard charts.
// Intent: malformed metrics requests must fail before repository access to
// prevent accidental unscoped metric scans.
// Intent: malformed Work Caller metrics requests must fail before repository
// access to prevent broad dashboard scans; a person's or an Application's
// usage is read by their own queries, never as a Work Caller's.
// Intent: Work metrics are scoped by Work authorization, not by a caller's
// own detail reconstruction or raw span enumeration.
// Intent: Work Caller metrics are scoped by the access decision of the
// extension defining the Work Caller's kind, never by Agent's Work access.
func TestListSpansBySession_RequiresWorkReadPermission(t *testing.T) {
	authorizer := &fakeTraceAuthorizer{}
	app := newTestApplication(fakeSpanQueryRepository{
		listBySessionFn: func(context.Context, query.SpanListOptions) (*query.SpanListResult, error) {
			return &query.SpanListResult{}, nil
		},
	}, authorizer)

	ctx := authn.WithAuthenticatedUser(authn.WithAuthenticatedUser(context.Background(), authn.AuthenticatedUser{ID: "test-reader"}), authn.AuthenticatedUser{ID: "user-1"})
	_, err := app.ListSpansBySession(ctx, &tracepublicv1.ListSpansBySessionRequest{WorkCaller: &agentpublicv1.WorkCaller{Kind: "team", Id: "team-1"}, AgentWorkId: "work-1"})
	if err != nil {
		t.Fatalf("ListSpansBySession: %v", err)
	}
	if authorizer.callerActor != "user-1" || authorizer.caller != teamCaller || authorizer.workID != "" {
		t.Fatalf("auth = %q for %+v, Work access for %q", authorizer.callerActor, authorizer.caller, authorizer.workID)
	}
}

func TestGetSpan_RequiresWorkReadPermissionForFetchedSpan(t *testing.T) {
	authorizer := &fakeTraceAuthorizer{}
	app := newTestApplication(fakeSpanQueryRepository{
		getFn: func(context.Context, query.SpanIdentity) (*query.Span, error) {
			return &query.Span{TraceID: "trace-1", SpanID: "span-1", WorkCaller: teamCaller, AgentWorkID: "work-1"}, nil
		},
	}, authorizer)

	ctx := authn.WithAuthenticatedUser(authn.WithAuthenticatedUser(context.Background(), authn.AuthenticatedUser{ID: "test-reader"}), authn.AuthenticatedUser{ID: "user-1"})
	_, err := app.GetSpan(ctx, &tracepublicv1.GetSpanRequest{WorkCaller: &agentpublicv1.WorkCaller{Kind: "team", Id: "team-1"}, TraceId: "trace-1", SpanId: "span-1"})
	if err != nil {
		t.Fatalf("GetSpan: %v", err)
	}
	if authorizer.callerActor != "user-1" || authorizer.caller != teamCaller || authorizer.workID != "" {
		t.Fatalf("auth = %q for %+v, Work access for %q", authorizer.callerActor, authorizer.caller, authorizer.workID)
	}
}

// Intent: spans looked up under a Work Caller without a Work authorize against
// the Work Caller instead of being denied only because agent_work_id is empty.
func TestGetSpan_RequiresWorkCallerReadPermissionWithoutWork(t *testing.T) {
	authorizer := &fakeTraceAuthorizer{}
	app := newTestApplication(fakeSpanQueryRepository{
		getFn: func(context.Context, query.SpanIdentity) (*query.Span, error) {
			return &query.Span{
				SpanID:         "span-1",
				TraceID:        "trace-1",
				WorkCaller:     teamCaller,
				RuntimePurpose: "maintainer",
				OwnerContext:   "caller",
				OwnerID:        "board-1",
			}, nil
		},
	}, authorizer)

	ctx := authn.WithAuthenticatedUser(authn.WithAuthenticatedUser(context.Background(), authn.AuthenticatedUser{ID: "test-reader"}), authn.AuthenticatedUser{ID: "user-1"})
	_, err := app.GetSpan(ctx, &tracepublicv1.GetSpanRequest{WorkCaller: &agentpublicv1.WorkCaller{Kind: "team", Id: "team-1"}, TraceId: "trace-1", SpanId: "span-1"})
	if err != nil {
		t.Fatalf("GetSpan: %v", err)
	}
	if authorizer.callerActor != "user-1" || authorizer.caller != teamCaller || authorizer.workID != "" {
		t.Fatalf("auth = %q for %+v, Work access for %q", authorizer.callerActor, authorizer.caller, authorizer.workID)
	}
}

// Intent: a span that does not carry the authorized Work Caller is not
// exposed, because Trace cannot prove its scope.
func TestGetSpan_SpanOutsideTheWorkCaller_ReturnsPermissionDenied(t *testing.T) {
	app := newTestApplication(fakeSpanQueryRepository{
		getFn: func(context.Context, query.SpanIdentity) (*query.Span, error) {
			return &query.Span{TraceID: "trace-1", SpanID: "span-1", RuntimePurpose: "maintainer"}, nil
		},
	}, &fakeTraceAuthorizer{})

	ctx := authn.WithAuthenticatedUser(authn.WithAuthenticatedUser(context.Background(), authn.AuthenticatedUser{ID: "test-reader"}), authn.AuthenticatedUser{ID: "user-1"})
	_, err := app.GetSpan(ctx, &tracepublicv1.GetSpanRequest{WorkCaller: &agentpublicv1.WorkCaller{Kind: "team", Id: "team-1"}, TraceId: "trace-1", SpanId: "span-1"})
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("code=%s, want PermissionDenied (err=%v)", connect.CodeOf(err), err)
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// Without Agent's Work access no Standalone Work is read.
func TestMissingAuthorityCannotReadSpans(t *testing.T) {
	app := newTestApplication(fakeSpanQueryRepository{listBySessionFn: func(context.Context, query.SpanListOptions) (*query.SpanListResult, error) {
		t.Fatal("must not query spans without authority")
		return nil, nil
	}}, nil)
	_, err := app.ListSpansBySession(authn.WithAuthenticatedUser(context.Background(), authn.AuthenticatedUser{ID: "reader"}), &tracepublicv1.ListSpansBySessionRequest{AgentWorkId: "private"})
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("missing authority: %v", err)
	}
}
func TestMissingIdentityCannotReadSpans(t *testing.T) {
	app := newTestApplication(fakeSpanQueryRepository{listBySessionFn: func(context.Context, query.SpanListOptions) (*query.SpanListResult, error) {
		t.Fatal("must not query spans without identity")
		return nil, nil
	}})
	_, err := app.ListSpansBySession(context.Background(), &tracepublicv1.ListSpansBySessionRequest{WorkCaller: &agentpublicv1.WorkCaller{Kind: "team", Id: "team-1"}, AgentWorkId: "private"})
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("missing identity: %v", err)
	}
}
