package connectrpc

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"
	tracepublicv1 "github.com/talgent-work/workbench/gen/go/talgent/trace/public/v1"
	"github.com/talgent-work/workbench/gen/go/talgent/trace/public/v1/tracepublicv1connect"
	"github.com/talgent-work/workbench/internal/business/trace/application"
	"github.com/talgent-work/workbench/internal/business/trace/application/query"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Handler struct{ app *application.Application }

func NewHandler(app *application.Application) *Handler { return &Handler{app: app} }

var _ tracepublicv1connect.TraceQueryServiceHandler = (*Handler)(nil)

func (h *Handler) ListSpansBySession(ctx context.Context, req *connect.Request[tracepublicv1.ListSpansBySessionRequest]) (*connect.Response[tracepublicv1.ListSpansBySessionResponse], error) {
	result, err := h.app.Queries.ListSpansBySession(ctx, query.SpanListOptions{
		ProjectID: req.Msg.GetProjectId(), AgentWorkID: req.Msg.GetAgentWorkId(),
		Limit: int(req.Msg.GetLimit()), AfterCursor: req.Msg.GetAfterCursor(), NewestFirst: req.Msg.GetNewestFirst(),
	})
	if err != nil {
		return nil, mapError(err)
	}
	if result == nil {
		result = &query.SpanListResult{}
	}
	return connect.NewResponse(&tracepublicv1.ListSpansBySessionResponse{Spans: spansToProto(result.Spans), NextCursor: result.NextCursor}), nil
}

func (h *Handler) ListSpansByOwner(ctx context.Context, req *connect.Request[tracepublicv1.ListSpansByOwnerRequest]) (*connect.Response[tracepublicv1.ListSpansByOwnerResponse], error) {
	result, err := h.app.Queries.ListSpansByOwner(ctx, query.SpanListOptions{
		ProjectID: req.Msg.GetProjectId(), OwnerContext: req.Msg.GetOwnerContext(), OwnerID: req.Msg.GetOwnerId(), RuntimePurpose: req.Msg.GetRuntimePurpose(),
		Limit: int(req.Msg.GetLimit()), AfterCursor: req.Msg.GetAfterCursor(),
	})
	if err != nil {
		return nil, mapError(err)
	}
	if result == nil {
		result = &query.SpanListResult{}
	}
	return connect.NewResponse(&tracepublicv1.ListSpansByOwnerResponse{Spans: spansToProto(result.Spans), NextCursor: result.NextCursor}), nil
}

func (h *Handler) GetSpan(ctx context.Context, req *connect.Request[tracepublicv1.GetSpanRequest]) (*connect.Response[tracepublicv1.GetSpanResponse], error) {
	span, err := h.app.Queries.GetSpan(ctx, query.SpanIdentity{ProjectID: req.Msg.GetProjectId(), AgentWorkID: req.Msg.GetAgentWorkId(), TraceID: req.Msg.GetTraceId(), SpanID: req.Msg.GetSpanId()})
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&tracepublicv1.GetSpanResponse{Span: spanToProto(span)}), nil
}

func (h *Handler) GetSessionTraceMetrics(ctx context.Context, req *connect.Request[tracepublicv1.GetSessionTraceMetricsRequest]) (*connect.Response[tracepublicv1.GetSessionTraceMetricsResponse], error) {
	if err := validateProtoTimes(req.Msg.GetStartTime(), req.Msg.GetEndTime()); err != nil {
		return nil, mapError(err)
	}
	result, err := h.app.Queries.GetSessionTraceMetrics(ctx, traceMetricOptionsFromSessionRequest(req.Msg.GetProjectId(), req.Msg.GetAgentWorkId(), req.Msg))
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(sessionTraceMetricsToProto(result)), nil
}

func (h *Handler) GetProjectTraceMetrics(ctx context.Context, req *connect.Request[tracepublicv1.GetProjectTraceMetricsRequest]) (*connect.Response[tracepublicv1.GetProjectTraceMetricsResponse], error) {
	if err := validateProtoTimes(req.Msg.GetStartTime(), req.Msg.GetEndTime()); err != nil {
		return nil, mapError(err)
	}
	result, err := h.app.Queries.GetProjectTraceMetrics(ctx, traceMetricOptionsFromProjectRequest(req.Msg.GetProjectId(), req.Msg))
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(projectTraceMetricsToProto(result)), nil
}

func (h *Handler) GetPersonalTraceMetrics(ctx context.Context, req *connect.Request[tracepublicv1.GetPersonalTraceMetricsRequest]) (*connect.Response[tracepublicv1.GetPersonalTraceMetricsResponse], error) {
	if err := validateProtoTimes(req.Msg.GetStartTime(), req.Msg.GetEndTime()); err != nil {
		return nil, mapError(err)
	}
	result, err := h.app.Queries.GetPersonalTraceMetrics(ctx, traceMetricOptionsFromPersonalRequest(req.Msg))
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(personalTraceMetricsToProto(result)), nil
}

func mapError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, query.ErrInvalidQuery), errors.Is(err, query.ErrInvalidSpanCursor):
		return connect.NewError(connect.CodeInvalidArgument, err)
	case errors.Is(err, query.ErrSpanNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, query.ErrAuthorizationUnauthenticated):
		return connect.NewError(connect.CodeUnauthenticated, query.ErrAuthorizationUnauthenticated)
	case errors.Is(err, query.ErrAuthorizationUnavailable):
		return connect.NewError(connect.CodeUnavailable, query.ErrAuthorizationUnavailable)
	case errors.Is(err, query.ErrAuthorizationPermissionDenied):
		return connect.NewError(connect.CodePermissionDenied, query.ErrAuthorizationPermissionDenied)
	default:
		return connect.NewError(connect.CodeInternal, err)
	}
}

func (h *Handler) ListEvents(ctx context.Context, req *connect.Request[tracepublicv1.ListEventsRequest]) (*connect.Response[tracepublicv1.ListEventsResponse], error) {
	if err := validateProtoTimes(req.Msg.GetStartTime(), req.Msg.GetEndTime()); err != nil {
		return nil, mapError(err)
	}
	result, err := h.app.Queries.ListEvents(ctx, query.EventOptions{ProjectID: req.Msg.GetProjectId(), AgentWorkID: req.Msg.GetAgentWorkId(), TraceID: req.Msg.GetTraceId(), CallID: req.Msg.GetCallId(), StartTime: protoTime(req.Msg.GetStartTime()), EndTime: protoTime(req.Msg.GetEndTime()), Limit: int(req.Msg.GetLimit()), AfterCursor: req.Msg.GetAfterCursor()})
	if err != nil {
		return nil, mapError(err)
	}
	response := &tracepublicv1.ListEventsResponse{NextCursor: result.NextCursor}
	for _, e := range result.Events {
		response.Events = append(response.Events, &tracepublicv1.NativeEvent{Id: e.ID, Timestamp: timestampOrNil(e.Timestamp), TraceId: e.TraceID, SpanId: e.SpanID, Name: e.Name, Body: e.Body, Attributes: publicSpanAttributes(e.Attributes), ResourceAttributes: publicSpanAttributes(e.ResourceAttributes), ProjectId: e.ProjectID, AgentWorkId: e.AgentWorkID})
	}
	return connect.NewResponse(response), nil
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
