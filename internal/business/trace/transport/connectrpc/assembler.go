package connectrpc

import (
	"time"

	agentpublicv1 "github.com/talgent-work/contracts/gen/go/agent/public/v1"
	tracepublicv1 "github.com/talgent-work/contracts/gen/go/trace/public/v1"
	"github.com/talgent-work/workbench/internal/business/trace/application/query"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// projectWorkCallerKind is the Work Caller kind Observability's Project scope
// still stands for until it reads Work Callers of every extension kind.
const projectWorkCallerKind = "project"

// projectScope is the Project scope a Work Caller names: its identity when it
// is a Project, else none.
func projectScope(caller *agentpublicv1.WorkCaller) string {
	if caller.GetKind() != projectWorkCallerKind {
		return ""
	}
	return caller.GetId()
}

// projectWorkCaller is the Work Caller of a Project scope; nil for none.
func projectWorkCaller(projectID string) *agentpublicv1.WorkCaller {
	if projectID == "" {
		return nil
	}
	return &agentpublicv1.WorkCaller{Kind: projectWorkCallerKind, Id: projectID}
}

func spanToProto(sp *query.Span) *tracepublicv1.Span {
	if sp == nil {
		return nil
	}
	return &tracepublicv1.Span{
		SpanId:             sp.SpanID,
		TraceId:            sp.TraceID,
		ParentSpanId:       sp.ParentSpanID,
		Name:               sp.Name,
		Kind:               sp.Kind,
		StartTime:          timestampOrNil(sp.StartTime),
		EndTime:            timestampOrNil(sp.EndTime),
		DurationNanos:      sp.DurationNanos,
		StatusCode:         sp.StatusCode,
		StatusMessage:      sp.StatusMessage,
		Attributes:         publicSpanAttributes(sp.Attributes),
		ResourceAttributes: publicSpanAttributes(sp.ResourceAttributes),
		Category:           sp.Category, CallId: sp.CallID, NativeAgentId: sp.NativeAgentID, ParentNativeAgentId: sp.ParentNativeAgentID,
		AgentWorkId:    sp.AgentWorkID,
		WorkCaller:     projectWorkCaller(sp.ProjectID),
		RuntimePurpose: sp.RuntimePurpose,
		OwnerContext:   sp.OwnerContext,
		OwnerId:        sp.OwnerID,
	}
}

func spansToProto(spans []*query.Span) []*tracepublicv1.Span {
	out := make([]*tracepublicv1.Span, 0, len(spans))
	for _, sp := range spans {
		if sp == nil {
			continue
		}
		out = append(out, spanToProto(sp))
	}
	return out
}

func traceMetricOptionsFromSessionRequest(projectID, sessionID string, req *tracepublicv1.GetSessionTraceMetricsRequest) query.TraceMetricOptions {
	return query.TraceMetricOptions{
		ProjectID: projectID, AgentWorkID: sessionID,
		StartTime:   protoTime(req.GetStartTime()),
		EndTime:     protoTime(req.GetEndTime()),
		StepSeconds: int(req.GetStepSeconds()),
		MetricKinds: cloneStrings(req.GetMetricKinds()),
	}
}

func traceMetricOptionsFromPersonalRequest(req *tracepublicv1.GetPersonalTraceMetricsRequest) query.TraceMetricOptions {
	return query.TraceMetricOptions{
		AgentWorkID: req.GetAgentWorkId(),
		StartTime:   protoTime(req.GetStartTime()),
		EndTime:     protoTime(req.GetEndTime()),
		StepSeconds: int(req.GetStepSeconds()),
		MetricKinds: cloneStrings(req.GetMetricKinds()),
	}
}

func traceMetricOptionsFromProjectRequest(projectID string, req *tracepublicv1.GetWorkCallerTraceMetricsRequest) query.TraceMetricOptions {
	return query.TraceMetricOptions{
		ProjectID:   projectID,
		StartTime:   protoTime(req.GetStartTime()),
		EndTime:     protoTime(req.GetEndTime()),
		StepSeconds: int(req.GetStepSeconds()),
		MetricKinds: cloneStrings(req.GetMetricKinds()),
	}
}

// traceMetricsToProto maps a metric result to the buckets and totals every
// metrics response carries.
func traceMetricsToProto(result *query.TraceMetricResult) ([]*tracepublicv1.TraceMetricBucket, []*tracepublicv1.TraceMetricTotal) {
	if result == nil {
		return nil, nil
	}
	buckets := make([]*tracepublicv1.TraceMetricBucket, 0, len(result.Buckets))
	for _, bucket := range result.Buckets {
		buckets = append(buckets, &tracepublicv1.TraceMetricBucket{
			BucketStart:    timestampOrNil(bucket.BucketStart),
			BucketSeconds:  int32(bucket.BucketSeconds),
			WorkCaller:     projectWorkCaller(bucket.ProjectID),
			AgentWorkId:    bucket.AgentWorkID,
			RuntimePurpose: bucket.RuntimePurpose,
			OwnerContext:   bucket.OwnerContext,
			OwnerId:        bucket.OwnerID,
			UserId:         bucket.UserID,
			MetricKind:     bucket.MetricKind,
			Dimension:      bucket.Dimension,
			Value:          bucket.Value,
			UnknownCount:   bucket.UnknownCount,
			Gaps:           bucket.Gaps,
		})
	}
	return buckets, totalsToProto(result.Totals)
}

func totalsToProto(in []query.TraceMetricTotal) []*tracepublicv1.TraceMetricTotal {
	totals := make([]*tracepublicv1.TraceMetricTotal, 0, len(in))
	for _, total := range in {
		totals = append(totals, &tracepublicv1.TraceMetricTotal{
			MetricKind:   total.MetricKind,
			Dimension:    total.Dimension,
			Value:        total.Value,
			UnknownCount: total.UnknownCount,
		})
	}
	return totals
}

func applicationUsageToProto(usage *query.ApplicationUsage) *tracepublicv1.GetApplicationTraceMetricsResponse {
	buckets, totals := traceMetricsToProto(&usage.TraceMetricResult)
	response := &tracepublicv1.GetApplicationTraceMetricsResponse{Buckets: buckets, Totals: totals, LabelKeys: usage.LabelKeys}
	for _, group := range usage.Breakdown {
		response.Breakdown = append(response.Breakdown, &tracepublicv1.LabelUsage{Value: group.Value, Labelled: group.Labelled, Totals: totalsToProto(group.Totals), WorkCount: int32(group.WorkCount)})
	}
	return response
}

func sessionTraceMetricsToProto(result *query.TraceMetricResult) *tracepublicv1.GetSessionTraceMetricsResponse {
	buckets, totals := traceMetricsToProto(result)
	return &tracepublicv1.GetSessionTraceMetricsResponse{Buckets: buckets, Totals: totals}
}

func projectTraceMetricsToProto(result *query.TraceMetricResult) *tracepublicv1.GetWorkCallerTraceMetricsResponse {
	buckets, totals := traceMetricsToProto(result)
	return &tracepublicv1.GetWorkCallerTraceMetricsResponse{Buckets: buckets, Totals: totals}
}

func personalTraceMetricsToProto(result *query.TraceMetricResult) *tracepublicv1.GetPersonalTraceMetricsResponse {
	buckets, totals := traceMetricsToProto(result)
	return &tracepublicv1.GetPersonalTraceMetricsResponse{Buckets: buckets, Totals: totals}
}

func protoTime(ts *timestamppb.Timestamp) time.Time {
	if ts == nil {
		return time.Time{}
	}
	return ts.AsTime()
}

func cloneStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	out = append(out, in...)
	return out
}

func timestampOrNil(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func publicSpanAttributes(attributes map[string]string) map[string]string {
	result := cloneStringMap(attributes)
	delete(result, "talgent.agent_id")
	delete(result, "talgent.work_id")
	delete(result, "talgent.application_id")
	return result
}
