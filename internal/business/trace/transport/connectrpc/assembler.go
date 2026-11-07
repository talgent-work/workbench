package connectrpc

import (
	"time"

	tracepublicv1 "github.com/talgent-work/contracts/gen/go/trace/public/v1"
	"github.com/talgent-work/workbench/internal/business/trace/application/query"
	"google.golang.org/protobuf/types/known/timestamppb"
)

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
		ProjectId:      sp.ProjectID,
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

func traceMetricOptionsFromProjectRequest(projectID string, req *tracepublicv1.GetProjectTraceMetricsRequest) query.TraceMetricOptions {
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
			ProjectId:      bucket.ProjectID,
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
	totals := make([]*tracepublicv1.TraceMetricTotal, 0, len(result.Totals))
	for _, total := range result.Totals {
		totals = append(totals, &tracepublicv1.TraceMetricTotal{
			MetricKind:   total.MetricKind,
			Dimension:    total.Dimension,
			Value:        total.Value,
			UnknownCount: total.UnknownCount,
		})
	}
	return buckets, totals
}

func sessionTraceMetricsToProto(result *query.TraceMetricResult) *tracepublicv1.GetSessionTraceMetricsResponse {
	buckets, totals := traceMetricsToProto(result)
	return &tracepublicv1.GetSessionTraceMetricsResponse{Buckets: buckets, Totals: totals}
}

func projectTraceMetricsToProto(result *query.TraceMetricResult) *tracepublicv1.GetProjectTraceMetricsResponse {
	buckets, totals := traceMetricsToProto(result)
	return &tracepublicv1.GetProjectTraceMetricsResponse{Buckets: buckets, Totals: totals}
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
	return result
}
