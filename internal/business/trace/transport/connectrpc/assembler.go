package connectrpc

import (
	"time"

	agentpublicv1 "github.com/talgent-work/contracts/gen/go/agent/public/v1"
	tracepublicv1 "github.com/talgent-work/contracts/gen/go/trace/public/v1"
	"github.com/talgent-work/workbench/internal/business/trace/application/query"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// workCallerFromProto is the Work Caller a request names; nil names none.
func workCallerFromProto(caller *agentpublicv1.WorkCaller) query.WorkCaller {
	return query.WorkCaller{Kind: caller.GetKind(), ID: caller.GetId()}
}

// workKeyFromProto is the Work Key a request names; nil names none.
func workKeyFromProto(key *agentpublicv1.WorkKey) query.WorkKey {
	return query.WorkKey{Kind: key.GetKind(), ID: key.GetId()}
}

// workCallerToProto is a Work's Work Caller; nil for none.
func workCallerToProto(caller query.WorkCaller) *agentpublicv1.WorkCaller {
	if !caller.Present() {
		return nil
	}
	return &agentpublicv1.WorkCaller{Kind: caller.Kind, Id: caller.ID}
}

// workKeyToProto is a Work's Work Key; nil for a Work without one.
func workKeyToProto(key query.WorkKey) *agentpublicv1.WorkKey {
	if !key.Present() {
		return nil
	}
	return &agentpublicv1.WorkKey{Kind: key.Kind, Id: key.ID}
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
		WorkCaller:     workCallerToProto(sp.WorkCaller),
		WorkKey:        workKeyToProto(sp.WorkKey),
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

func traceMetricOptionsFromSessionRequest(req *tracepublicv1.GetSessionTraceMetricsRequest) query.TraceMetricOptions {
	return query.TraceMetricOptions{
		WorkCaller: workCallerFromProto(req.GetWorkCaller()), AgentWorkID: req.GetAgentWorkId(),
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

func traceMetricOptionsFromWorkCallerRequest(req *tracepublicv1.GetWorkCallerTraceMetricsRequest) query.TraceMetricOptions {
	return query.TraceMetricOptions{
		WorkCaller:  workCallerFromProto(req.GetWorkCaller()),
		WorkKey:     workKeyFromProto(req.GetWorkKey()),
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
			WorkCaller:     workCallerToProto(bucket.WorkCaller),
			WorkKey:        workKeyToProto(bucket.WorkKey),
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

func workCallerTraceMetricsToProto(result *query.TraceMetricResult) *tracepublicv1.GetWorkCallerTraceMetricsResponse {
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
