package connectrpc

import (
	"context"
	"time"

	"github.com/talgent-work/workbench/internal/business/trace/application"
	"github.com/talgent-work/workbench/internal/business/trace/application/query"
)

// teamCaller is the Work Caller of an extension-defined kind the tests read
// Extension Works under.
var teamCaller = query.WorkCaller{Kind: "team", ID: "team-1"}

type fakeSpanQueryRepository struct {
	listBySessionFn    func(context.Context, query.SpanListOptions) (*query.SpanListResult, error)
	listByWorkCallerFn func(context.Context, query.SpanListOptions) (*query.SpanListResult, error)
	listFn             func(context.Context, string, int) ([]*query.Span, error)
	getFn              func(context.Context, query.SpanIdentity) (*query.Span, error)
}

type fakeTraceMetricRepository struct {
	getBySessionFn    func(context.Context, query.TraceMetricOptions) (*query.TraceMetricResult, error)
	getByWorkCallerFn func(context.Context, query.TraceMetricOptions) (*query.TraceMetricResult, error)
	getByPersonFn     func(context.Context, string, query.TraceMetricOptions) (*query.TraceMetricResult, error)
	// works are the Works a Work Caller has measurements of.
	works []string
	// callers are the Work Callers measured Works carry; a Work left out has
	// no measurements.
	callers map[string]query.WorkCaller
}

// fakeTraceAuthorizer stands for the extensions' Work Caller access
// decisions, Agent and Account, recording whom each was asked about.
type fakeTraceAuthorizer struct {
	callerActor string
	caller      query.WorkCaller
	callerErr   error
	workActor   string
	workID      string
	workErr     error
	// attributions are the Works each Application owns with their labels.
	attributions map[string]map[string]map[string]string
	// owners are the Application Owners Account answers.
	owners map[string]string
}

func (f *fakeTraceAuthorizer) ListWorkAttributions(_ context.Context, applicationID string, workIDs []string) ([]query.WorkAttribution, error) {
	var works []query.WorkAttribution
	for _, id := range workIDs {
		if labels, ok := f.attributions[applicationID][id]; ok {
			works = append(works, query.WorkAttribution{WorkID: id, Labels: labels})
		}
	}
	return works, nil
}

func (f *fakeTraceAuthorizer) GetApplication(_ context.Context, applicationID string) (query.OwnedApplication, error) {
	owner, ok := f.owners[applicationID]
	if !ok {
		return query.OwnedApplication{}, query.ErrApplicationUnknown
	}
	return query.OwnedApplication{ID: applicationID, OwnerUserID: owner}, nil
}

func (f *fakeTraceAuthorizer) AuthorizeWorkCallerReader(_ context.Context, readerID string, caller query.WorkCaller) error {
	f.callerActor, f.caller = readerID, caller
	return f.callerErr
}

func (f *fakeTraceAuthorizer) AuthorizeWorkReader(_ context.Context, readerID, workID string) error {
	f.workActor, f.workID = readerID, workID
	return f.workErr
}

// fakeEvidence is the native evidence of spans and events together.
type fakeEvidence struct {
	fakeSpanQueryRepository
	fakeEventRepository
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

func (f fakeSpanQueryRepository) ListByWorkCaller(ctx context.Context, opts query.SpanListOptions) (*query.SpanListResult, error) {
	if f.listByWorkCallerFn != nil {
		return f.listByWorkCallerFn(ctx, opts)
	}
	return &query.SpanListResult{}, nil
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

func (f fakeTraceMetricRepository) GetByWorkCaller(ctx context.Context, opts query.TraceMetricOptions) (*query.TraceMetricResult, error) {
	if f.getByWorkCallerFn != nil {
		return f.getByWorkCallerFn(ctx, opts)
	}
	return &query.TraceMetricResult{}, nil
}

func (f fakeTraceMetricRepository) GetByPerson(ctx context.Context, personID string, opts query.TraceMetricOptions) (*query.TraceMetricResult, error) {
	if f.getByPersonFn != nil {
		return f.getByPersonFn(ctx, personID, opts)
	}
	return nil, nil
}

func (f fakeTraceMetricRepository) ListWorkCallerWorks(_ context.Context, _ query.WorkCaller, _, _ time.Time, limit int) ([]string, error) {
	return f.works[:min(len(f.works), limit)], nil
}

func (f fakeTraceMetricRepository) GetWorkCaller(_ context.Context, workID string) (query.WorkCaller, bool, error) {
	caller, ok := f.callers[workID]
	return caller, ok, nil
}

func newTestApplication(repo fakeSpanQueryRepository, authorities ...*fakeTraceAuthorizer) *Handler {
	return newTestApplicationWithMetrics(repo, fakeTraceMetricRepository{}, authorities...)
}

// newTestApplicationWithMetrics admits every reader unless given an authority;
// an explicit nil authority leaves both decisions unavailable.
func newTestApplicationWithMetrics(repo fakeSpanQueryRepository, metrics fakeTraceMetricRepository, authorities ...*fakeTraceAuthorizer) *Handler {
	return newTestApplicationWithEvents(repo, metrics, fakeEventRepository{}, authorities...)
}

func newTestApplicationWithEvents(repo fakeSpanQueryRepository, metrics fakeTraceMetricRepository, events fakeEventRepository, authorities ...*fakeTraceAuthorizer) *Handler {
	authority := &fakeTraceAuthorizer{}
	if len(authorities) > 0 {
		authority = authorities[0]
	}
	evidence := fakeEvidence{repo, events}
	if authority == nil {
		return NewHandler(application.NewApplication(query.NewQueries(evidence, metrics, nil, nil, nil)))
	}
	return NewHandler(application.NewApplication(query.NewQueries(evidence, metrics, authority, authority, authority)))
}

func sampleSpan(start time.Time) *query.Span {
	return &query.Span{
		WorkCaller:   teamCaller,
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
		WorkCaller:     teamCaller,
		AgentID:        "runtime-1",
		RuntimePurpose: "maintainer",
		OwnerContext:   "caller",
		OwnerID:        "board-1",
		Name:           "tool_call:Read",
		Kind:           "Internal",
		StartTime:      start,
		EndTime:        start.Add(time.Second),
		DurationNanos:  int64(time.Second),
		StatusCode:     "Ok",
		Attributes: map[string]string{
			"talgent.work_caller":     "team:team-1",
			"talgent.agent_id":        "runtime-1",
			"talgent.runtime_purpose": "maintainer",
			"talgent.owner_context":   "caller",
			"talgent.owner_id":        "board-1",
		},
	}
}
