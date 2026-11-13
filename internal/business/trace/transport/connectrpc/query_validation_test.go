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
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Invalid protobuf timestamps must not normalize silently into a different
// query interval; reversed/empty intervals and negative steps are invalid too.
func TestQueryRejectsInvalidIntervals(t *testing.T) {
	now := timestamppb.New(time.Now())
	for _, test := range []struct {
		name       string
		start, end *timestamppb.Timestamp
	}{
		{"invalid start nanos", &timestamppb.Timestamp{Seconds: now.Seconds, Nanos: 1000000000}, nil},
		{"invalid end seconds", nil, &timestamppb.Timestamp{Seconds: 253402300800}},
		{"empty interval", now, now},
		{"reversed interval", now, timestamppb.New(now.AsTime().Add(-time.Hour))},
	} {
		t.Run(test.name, func(t *testing.T) {
			// No authority or storage is needed: input rejection precedes either.
			h := newTestApplication(fakeSpanQueryRepository{})
			ctx := context.Background()
			_, sessionErr := h.GetSessionTraceMetrics(ctx, &tracepublicv1.GetSessionTraceMetricsRequest{WorkCaller: &agentpublicv1.WorkCaller{Kind: "team", Id: "t"}, AgentWorkId: "s", StartTime: test.start, EndTime: test.end})
			_, callerErr := h.GetWorkCallerTraceMetrics(ctx, &tracepublicv1.GetWorkCallerTraceMetricsRequest{WorkCaller: &agentpublicv1.WorkCaller{Kind: "team", Id: "t"}, StartTime: test.start, EndTime: test.end})
			_, eventErr := h.ListEvents(ctx, &tracepublicv1.ListEventsRequest{WorkCaller: &agentpublicv1.WorkCaller{Kind: "team", Id: "t"}, StartTime: test.start, EndTime: test.end})
			for name, err := range map[string]error{"session": sessionErr, "work caller": callerErr, "events": eventErr} {
				if connect.CodeOf(err) != connect.CodeInvalidArgument {
					t.Errorf("%s code = %s, error = %v", name, connect.CodeOf(err), err)
				}
			}
		})
	}
	h := newTestApplication(fakeSpanQueryRepository{})
	_, err := h.GetWorkCallerTraceMetrics(context.Background(), &tracepublicv1.GetWorkCallerTraceMetricsRequest{WorkCaller: &agentpublicv1.WorkCaller{Kind: "team", Id: "t"}, StepSeconds: -1})
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("negative step code = %s", connect.CodeOf(err))
	}
}

func TestSpanLookupRequiresCompleteIdentityAndWorkCallerAdmission(t *testing.T) {
	ctx := authn.WithAuthenticatedUser(context.Background(), authn.AuthenticatedUser{ID: "reader"})
	repo := fakeSpanQueryRepository{getFn: func(context.Context, query.SpanIdentity) (*query.Span, error) {
		t.Fatal("invalid or denied lookup reached storage")
		return nil, nil
	}}
	h := newTestApplication(repo)
	for _, req := range []*tracepublicv1.GetSpanRequest{
		{SpanId: "span"}, {WorkCaller: &agentpublicv1.WorkCaller{Kind: "team", Id: "team"}, SpanId: "span"}, {TraceId: "trace", SpanId: "span"},
	} {
		if _, err := h.GetSpan(ctx, req); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("incomplete identity admitted: %v", err)
		}
	}
	h = newTestApplication(repo, &fakeTraceAuthorizer{callerErr: query.ErrAuthorizationPermissionDenied})
	_, err := h.GetSpan(ctx, &tracepublicv1.GetSpanRequest{WorkCaller: &agentpublicv1.WorkCaller{Kind: "team", Id: "team"}, TraceId: "trace", SpanId: "span"})
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("denied Work Caller admitted: %v", err)
	}
}
