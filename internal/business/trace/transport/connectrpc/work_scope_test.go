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

// workCalls reads one Work through every per-Work read, named under caller,
// or as a Standalone Work when caller is nil.
func workCalls(app *Handler, ctx context.Context, caller *agentpublicv1.WorkCaller, work string) map[string]func() error {
	return map[string]func() error{
		"spans": func() error {
			_, err := app.ListSpansBySession(ctx, &tracepublicv1.ListSpansBySessionRequest{WorkCaller: caller, AgentWorkId: work})
			return err
		},
		"metrics": func() error {
			_, err := app.GetSessionTraceMetrics(ctx, &tracepublicv1.GetSessionTraceMetricsRequest{WorkCaller: caller, AgentWorkId: work})
			return err
		},
		"events": func() error {
			_, err := app.ListEvents(ctx, &tracepublicv1.ListEventsRequest{WorkCaller: caller, AgentWorkId: work})
			return err
		},
		"span": func() error {
			_, err := app.GetSpan(ctx, &tracepublicv1.GetSpanRequest{WorkCaller: caller, AgentWorkId: work, TraceId: "trace-1", SpanId: "span-1"})
			return err
		},
	}
}

// A Standalone Work is named without a Work Caller and read under Agent's
// Work access, never an extension's decision, within the Work Caller its
// measurements carry.
func TestStandaloneWorkIsReadUnderWorkAccess(t *testing.T) {
	owner := query.PersonCaller("owner")
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
			return &query.Span{TraceID: "trace-1", SpanID: "span-1", AgentWorkID: "work-1", WorkCaller: owner}, nil
		},
	}, fakeTraceMetricRepository{callers: map[string]query.WorkCaller{"work-1": owner}, getBySessionFn: func(_ context.Context, opts query.TraceMetricOptions) (*query.TraceMetricResult, error) {
		metricScope = opts
		return &query.TraceMetricResult{}, nil
	}}, fakeEventRepository{listFn: func(_ context.Context, opts query.EventOptions) (*query.EventResult, error) {
		eventScope = opts
		return &query.EventResult{}, nil
	}}, authority)
	ctx := authn.WithAuthenticatedUser(context.Background(), authn.AuthenticatedUser{ID: "owner"})

	for name, call := range workCalls(app, ctx, nil, "work-1") {
		if err := call(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if authority.workActor != "owner" || authority.workID != "work-1" || authority.caller.Present() {
		t.Fatalf("authority asked = %+v, want Work access for owner on work-1 only", authority)
	}
	if spanScope.WorkCaller != owner || metricScope.WorkCaller != owner || eventScope.WorkCaller != owner || spanIdentity.WorkCaller != owner || spanIdentity.AgentWorkID != "work-1" {
		t.Fatalf("scopes = %+v %+v %+v %+v, want the Work's evidence under its own Work Caller", spanScope, metricScope, eventScope, spanIdentity)
	}
}

// Someone who is neither the owner nor a Work Share holder reads nothing of a
// Standalone Work, and nothing is queried on their behalf.
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
	}, fakeTraceMetricRepository{callers: map[string]query.WorkCaller{"work-1": query.PersonCaller("owner")}, getBySessionFn: func(context.Context, query.TraceMetricOptions) (*query.TraceMetricResult, error) {
		t.Fatal("metrics read for a refused reader")
		return nil, nil
	}}, fakeEventRepository{listFn: func(context.Context, query.EventOptions) (*query.EventResult, error) {
		t.Fatal("events read for a refused reader")
		return nil, nil
	}}, authority)
	ctx := authn.WithAuthenticatedUser(context.Background(), authn.AuthenticatedUser{ID: "stranger"})
	for name, call := range workCalls(app, ctx, nil, "work-1") {
		if connect.CodeOf(call()) != connect.CodePermissionDenied {
			t.Errorf("%s: want PermissionDenied", name)
		}
	}
}

// An Extension Work named as a Standalone Work is NotFound, even to its
// owner whom Agent's Work access admits: only the extension's decision opens
// it. A Work named under a Work Caller other than its own is NotFound too.
func TestWorkNamedOutsideItsWorkCallerIsNotFound(t *testing.T) {
	refuseReads := fakeSpanQueryRepository{
		listBySessionFn: func(context.Context, query.SpanListOptions) (*query.SpanListResult, error) {
			t.Fatal("spans read outside the Work's scope")
			return nil, nil
		},
		getFn: func(context.Context, query.SpanIdentity) (*query.Span, error) {
			t.Fatal("span read outside the Work's scope")
			return nil, nil
		},
	}
	metrics := fakeTraceMetricRepository{callers: map[string]query.WorkCaller{"work-1": teamCaller}, getBySessionFn: func(context.Context, query.TraceMetricOptions) (*query.TraceMetricResult, error) {
		t.Fatal("metrics read outside the Work's scope")
		return nil, nil
	}}
	events := fakeEventRepository{listFn: func(context.Context, query.EventOptions) (*query.EventResult, error) {
		t.Fatal("events read outside the Work's scope")
		return nil, nil
	}}
	app := newTestApplicationWithEvents(refuseReads, metrics, events, &fakeTraceAuthorizer{})
	ctx := authn.WithAuthenticatedUser(context.Background(), authn.AuthenticatedUser{ID: "owner"})
	for scope, caller := range map[string]*agentpublicv1.WorkCaller{
		"as a Standalone Work": nil,
		"under another caller": {Kind: "team", Id: "team-2"},
	} {
		for name, call := range workCalls(app, ctx, caller, "work-1") {
			if err := call(); connect.CodeOf(err) != connect.CodeNotFound {
				t.Errorf("%s %s: code = %s, want NotFound (err=%v)", name, scope, connect.CodeOf(err), err)
			}
		}
	}
}

// A Work named under a Work Caller it is not proven under answers NotFound
// alike, whether it carries another Work Caller or has no measurements, so a
// member of one Work Caller cannot probe which Works exist elsewhere.
func TestUnobservedWorkUnderAWorkCallerIsNotFound(t *testing.T) {
	app := newTestApplicationWithEvents(fakeSpanQueryRepository{}, fakeTraceMetricRepository{callers: map[string]query.WorkCaller{"elsewhere": {Kind: "team", ID: "team-2"}}}, fakeEventRepository{}, &fakeTraceAuthorizer{})
	ctx := authn.WithAuthenticatedUser(context.Background(), authn.AuthenticatedUser{ID: "member"})
	team := &agentpublicv1.WorkCaller{Kind: "team", Id: "team-1"}
	for _, work := range []string{"elsewhere", "unmeasured"} {
		for name, call := range workCalls(app, ctx, team, work) {
			if err := call(); connect.CodeOf(err) != connect.CodeNotFound {
				t.Errorf("%s of %s: code = %s, want NotFound (err=%v)", name, work, connect.CodeOf(err), err)
			}
		}
	}
}

// A requested Work Caller or Work Key is taken exactly as supplied: an id
// with surrounding whitespace or a control character is refused, never
// trimmed, before any authority is asked.
func TestRequestedWorkFactsAreNeverTrimmed(t *testing.T) {
	authority := &fakeTraceAuthorizer{}
	app := newTestApplication(fakeSpanQueryRepository{}, authority)
	ctx := authn.WithAuthenticatedUser(context.Background(), authn.AuthenticatedUser{ID: "member"})
	for _, id := range []string{" team-1", "team-1 ", "team\n1", "team\x001"} {
		_, err := app.ListSpansBySession(ctx, &tracepublicv1.ListSpansBySessionRequest{WorkCaller: &agentpublicv1.WorkCaller{Kind: "team", Id: id}, AgentWorkId: "work-1"})
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("work caller id %q: %v", id, err)
		}
		_, err = app.GetWorkCallerTraceMetrics(ctx, &tracepublicv1.GetWorkCallerTraceMetricsRequest{WorkCaller: &agentpublicv1.WorkCaller{Kind: "team", Id: "team-1"}, WorkKey: &agentpublicv1.WorkKey{Kind: "board", Id: id}})
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("work key id %q: %v", id, err)
		}
	}
	if authority.caller.Present() {
		t.Fatalf("authority asked for %+v", authority.caller)
	}
}

// An Extension Work is read under the access decision of the extension
// defining its Work Caller's kind, and an Application reads none.
func TestExtensionWorkIsReadUnderItsExtensionsDecision(t *testing.T) {
	authority := &fakeTraceAuthorizer{workErr: query.ErrAuthorizationPermissionDenied}
	var scope query.SpanListOptions
	app := newTestApplicationWithMetrics(fakeSpanQueryRepository{listBySessionFn: func(_ context.Context, opts query.SpanListOptions) (*query.SpanListResult, error) {
		scope = opts
		return &query.SpanListResult{}, nil
	}}, fakeTraceMetricRepository{callers: map[string]query.WorkCaller{"work-1": teamCaller}}, authority)
	member := authn.WithAuthenticatedUser(context.Background(), authn.AuthenticatedUser{ID: "member"})
	team := &agentpublicv1.WorkCaller{Kind: "team", Id: "team-1"}
	if _, err := app.ListSpansBySession(member, &tracepublicv1.ListSpansBySessionRequest{WorkCaller: team, AgentWorkId: "work-1"}); err != nil {
		t.Fatalf("member: %v", err)
	}
	if authority.callerActor != "member" || authority.caller != teamCaller || authority.workID != "" || scope.WorkCaller != teamCaller {
		t.Fatalf("authority %+v, scope %+v", authority, scope)
	}
	application := authn.WithPrincipal(context.Background(), authn.ApplicationPrincipal(authn.Application{ID: "application-1"}))
	if _, err := app.ListSpansBySession(application, &tracepublicv1.ListSpansBySessionRequest{WorkCaller: team, AgentWorkId: "work-1"}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("application reading an Extension Work: %v", err)
	}
}

// A Standalone Work with no measurements has nothing to disclose: its reads
// answer with nothing, and its span lookup is NotFound.
func TestUnobservedStandaloneWorkDisclosesNothing(t *testing.T) {
	app := newTestApplicationWithEvents(fakeSpanQueryRepository{}, fakeTraceMetricRepository{callers: map[string]query.WorkCaller{}}, fakeEventRepository{}, &fakeTraceAuthorizer{})
	ctx := authn.WithAuthenticatedUser(context.Background(), authn.AuthenticatedUser{ID: "owner"})
	spans, err := app.ListSpansBySession(ctx, &tracepublicv1.ListSpansBySessionRequest{AgentWorkId: "work-1"})
	if err != nil || len(spans.GetSpans()) != 0 {
		t.Fatalf("spans = %v, %v", spans, err)
	}
	metrics, err := app.GetSessionTraceMetrics(ctx, &tracepublicv1.GetSessionTraceMetricsRequest{AgentWorkId: "work-1"})
	if err != nil || len(metrics.GetBuckets()) != 0 {
		t.Fatalf("metrics = %v, %v", metrics, err)
	}
	if _, err := app.GetSpan(ctx, &tracepublicv1.GetSpanRequest{AgentWorkId: "work-1", TraceId: "trace-1", SpanId: "span-1"}); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("span: %v", err)
	}
}

// Without a Work Caller the Work names the scope: queries naming neither are
// refused before any authority or evidence is consulted, and a span fetched
// under one Work is not disclosed as another's.
func TestStandaloneQueriesNameTheWork(t *testing.T) {
	authority := &fakeTraceAuthorizer{}
	owner := query.PersonCaller("owner")
	app := newTestApplicationWithEvents(fakeSpanQueryRepository{getFn: func(context.Context, query.SpanIdentity) (*query.Span, error) {
		return &query.Span{TraceID: "trace-1", SpanID: "span-1", AgentWorkID: "another-work", WorkCaller: owner}, nil
	}}, fakeTraceMetricRepository{callers: map[string]query.WorkCaller{"work-1": owner}}, fakeEventRepository{}, authority)
	ctx := authn.WithAuthenticatedUser(context.Background(), authn.AuthenticatedUser{ID: "owner"})
	_, err := app.ListEvents(ctx, &tracepublicv1.ListEventsRequest{})
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("events naming neither: %v", err)
	}
	_, err = app.GetSpan(ctx, &tracepublicv1.GetSpanRequest{TraceId: "trace-1", SpanId: "span-1"})
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("span naming neither: %v", err)
	}
	if authority.workID != "" || authority.caller.Present() {
		t.Fatalf("authority consulted for an unscoped query: %+v", authority)
	}
	_, err = app.GetSpan(ctx, &tracepublicv1.GetSpanRequest{AgentWorkId: "work-1", TraceId: "trace-1", SpanId: "span-1"})
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("another Work's span: %v", err)
	}
}

// A person reads only their own usage across every Work they own, whatever
// its Work Caller: the person is the authenticated reader, never a request
// field, and no access decision is asked.
func TestPersonalMetricsReadTheReadersOwnUsage(t *testing.T) {
	authority := &fakeTraceAuthorizer{callerErr: query.ErrAuthorizationPermissionDenied, workErr: query.ErrAuthorizationPermissionDenied}
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	var person string
	var captured query.TraceMetricOptions
	app := newTestApplicationWithMetrics(fakeSpanQueryRepository{}, fakeTraceMetricRepository{
		getByPersonFn: func(_ context.Context, personID string, opts query.TraceMetricOptions) (*query.TraceMetricResult, error) {
			person, captured = personID, opts
			return &query.TraceMetricResult{Buckets: []query.TraceMetricBucket{
				{AgentWorkID: "work-1", WorkCaller: query.PersonCaller(personID), UserID: personID, MetricKind: "input_tokens", Value: 7},
				{AgentWorkID: "work-2", WorkCaller: teamCaller, WorkKey: query.WorkKey{Kind: "board", ID: "board-1"}, UserID: personID, MetricKind: "input_tokens", Value: 3},
			}, Totals: []query.TraceMetricTotal{{MetricKind: "input_tokens", Value: 10}}}, nil
		},
	}, authority)
	ctx := authn.WithAuthenticatedUser(context.Background(), authn.AuthenticatedUser{ID: "person-1"})
	response, err := app.GetPersonalTraceMetrics(ctx, &tracepublicv1.GetPersonalTraceMetricsRequest{
		StartTime: timestampOrNil(start), EndTime: timestampOrNil(start.Add(time.Hour)), StepSeconds: 300, AgentWorkId: " work-1 ",
	})
	if err != nil {
		t.Fatalf("GetPersonalTraceMetrics: %v", err)
	}
	if person != "person-1" || captured.AgentWorkID != "work-1" || captured.StepSeconds != 300 || !captured.StartTime.Equal(start) || captured.WorkCaller.Present() {
		t.Fatalf("read person %q with %+v", person, captured)
	}
	if authority.caller.Present() || authority.workID != "" {
		t.Fatalf("an access decision was asked: %+v", authority)
	}
	buckets := response.GetBuckets()
	if buckets[0].GetWorkCaller().GetKind() != "person" || buckets[1].GetWorkCaller().GetKind() != "team" || buckets[1].GetWorkKey().GetId() != "board-1" || buckets[0].GetWorkKey() != nil {
		t.Fatalf("buckets = %+v", buckets)
	}
	if response.GetTotals()[0].GetValue() != 10 {
		t.Fatalf("response = %+v", response)
	}
	_, err = app.GetPersonalTraceMetrics(context.Background(), &tracepublicv1.GetPersonalTraceMetricsRequest{})
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("anonymous personal usage: %v", err)
	}
}
