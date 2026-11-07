package connectrpc

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect/v2"
	tracepublicv1 "github.com/talgent-work/contracts/gen/go/trace/public/v1"
	"github.com/talgent-work/contracts/gen/go/trace/public/v1/tracepublicv1connect"
	"github.com/talgent-work/workbench/internal/business/trace/application"
	"github.com/talgent-work/workbench/internal/business/trace/application/query"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Handler struct{ app *application.Application }

func NewHandler(app *application.Application) *Handler { return &Handler{app: app} }

var _ tracepublicv1connect.TraceQueryServiceHandler = (*Handler)(nil)

func (h *Handler) ListSpansBySession(ctx context.Context, req *tracepublicv1.ListSpansBySessionRequest) (*tracepublicv1.ListSpansBySessionResponse, error) {
	result, err := h.app.Queries.ListSpansBySession(ctx, query.SpanListOptions{
		ProjectID: req.GetProjectId(), AgentWorkID: req.GetAgentWorkId(),
		Limit: int(req.GetLimit()), AfterCursor: req.GetAfterCursor(), NewestFirst: req.GetNewestFirst(),
	})
	if err != nil {
		return nil, mapError(err)
	}
	if result == nil {
		result = &query.SpanListResult{}
	}
	return &tracepublicv1.ListSpansBySessionResponse{Spans: spansToProto(result.Spans), NextCursor: result.NextCursor}, nil
}

func (h *Handler) ListSpansByOwner(ctx context.Context, req *tracepublicv1.ListSpansByOwnerRequest) (*tracepublicv1.ListSpansByOwnerResponse, error) {
	result, err := h.app.Queries.ListSpansByOwner(ctx, query.SpanListOptions{
		ProjectID: req.GetProjectId(), OwnerContext: req.GetOwnerContext(), OwnerID: req.GetOwnerId(), RuntimePurpose: req.GetRuntimePurpose(),
		Limit: int(req.GetLimit()), AfterCursor: req.GetAfterCursor(),
	})
	if err != nil {
		return nil, mapError(err)
	}
	if result == nil {
		result = &query.SpanListResult{}
	}
	return &tracepublicv1.ListSpansByOwnerResponse{Spans: spansToProto(result.Spans), NextCursor: result.NextCursor}, nil
}

func (h *Handler) GetSpan(ctx context.Context, req *tracepublicv1.GetSpanRequest) (*tracepublicv1.GetSpanResponse, error) {
	span, err := h.app.Queries.GetSpan(ctx, query.SpanIdentity{ProjectID: req.GetProjectId(), AgentWorkID: req.GetAgentWorkId(), TraceID: req.GetTraceId(), SpanID: req.GetSpanId()})
	if err != nil {
		return nil, mapError(err)
	}
	return &tracepublicv1.GetSpanResponse{Span: spanToProto(span)}, nil
}

func (h *Handler) GetSessionTraceMetrics(ctx context.Context, req *tracepublicv1.GetSessionTraceMetricsRequest) (*tracepublicv1.GetSessionTraceMetricsResponse, error) {
	if err := validateProtoTimes(req.GetStartTime(), req.GetEndTime()); err != nil {
		return nil, mapError(err)
	}
	result, err := h.app.Queries.GetSessionTraceMetrics(ctx, traceMetricOptionsFromSessionRequest(req.GetProjectId(), req.GetAgentWorkId(), req))
	if err != nil {
		return nil, mapError(err)
	}
	return sessionTraceMetricsToProto(result), nil
}

func (h *Handler) GetProjectTraceMetrics(ctx context.Context, req *tracepublicv1.GetProjectTraceMetricsRequest) (*tracepublicv1.GetProjectTraceMetricsResponse, error) {
	if err := validateProtoTimes(req.GetStartTime(), req.GetEndTime()); err != nil {
		return nil, mapError(err)
	}
	result, err := h.app.Queries.GetProjectTraceMetrics(ctx, traceMetricOptionsFromProjectRequest(req.GetProjectId(), req))
	if err != nil {
		return nil, mapError(err)
	}
	return projectTraceMetricsToProto(result), nil
}

func (h *Handler) GetPersonalTraceMetrics(ctx context.Context, req *tracepublicv1.GetPersonalTraceMetricsRequest) (*tracepublicv1.GetPersonalTraceMetricsResponse, error) {
	if err := validateProtoTimes(req.GetStartTime(), req.GetEndTime()); err != nil {
		return nil, mapError(err)
	}
	result, err := h.app.Queries.GetPersonalTraceMetrics(ctx, traceMetricOptionsFromPersonalRequest(req))
	if err != nil {
		return nil, mapError(err)
	}
	return personalTraceMetricsToProto(result), nil
}

func mapError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, query.ErrInvalidQuery), errors.Is(err, query.ErrInvalidSpanCursor):
		return connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	case errors.Is(err, query.ErrSpanNotFound):
		return connect.NewError(connect.CodeNotFound, err.Error()).WithCause(err)
	case errors.Is(err, query.ErrAuthorizationUnauthenticated):
		return connect.NewError(connect.CodeUnauthenticated, query.ErrAuthorizationUnauthenticated.Error()).WithCause(query.ErrAuthorizationUnauthenticated)
	case errors.Is(err, query.ErrAuthorizationUnavailable):
		return connect.NewError(connect.CodeUnavailable, query.ErrAuthorizationUnavailable.Error()).WithCause(query.ErrAuthorizationUnavailable)
	case errors.Is(err, query.ErrAuthorizationPermissionDenied):
		return connect.NewError(connect.CodePermissionDenied, query.ErrAuthorizationPermissionDenied.Error()).WithCause(query.ErrAuthorizationPermissionDenied)
	default:
		return connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
}

func (h *Handler) ListEvents(ctx context.Context, req *tracepublicv1.ListEventsRequest) (*tracepublicv1.ListEventsResponse, error) {
	if err := validateProtoTimes(req.GetStartTime(), req.GetEndTime()); err != nil {
		return nil, mapError(err)
	}
	result, err := h.app.Queries.ListEvents(ctx, query.EventOptions{ProjectID: req.GetProjectId(), AgentWorkID: req.GetAgentWorkId(), TraceID: req.GetTraceId(), CallID: req.GetCallId(), StartTime: protoTime(req.GetStartTime()), EndTime: protoTime(req.GetEndTime()), Limit: int(req.GetLimit()), AfterCursor: req.GetAfterCursor()})
	if err != nil {
		return nil, mapError(err)
	}
	response := &tracepublicv1.ListEventsResponse{NextCursor: result.NextCursor}
	for _, e := range result.Events {
		response.Events = append(response.Events, &tracepublicv1.NativeEvent{Id: e.ID, Timestamp: timestampOrNil(e.Timestamp), TraceId: e.TraceID, SpanId: e.SpanID, Name: e.Name, Body: e.Body, Attributes: publicSpanAttributes(e.Attributes), ResourceAttributes: publicSpanAttributes(e.ResourceAttributes), ProjectId: e.ProjectID, AgentWorkId: e.AgentWorkID})
	}
	return response, nil
}

func validateProtoTimes(times ...*timestamppb.Timestamp) error {
	for _, ts := range times {
		if ts != nil {
			if err := ts.CheckValid(); err != nil {
				return fmt.Errorf("%w: %v", query.ErrInvalidQuery, err)
			}
		}
	}
	return nil
}
