package connectrpc

import (
	"context"
	"errors"
	"net/http"
	"time"

	"connectrpc.com/connect"
	dispatcherpublicv1 "github.com/talgent-work/workbench/gen/go/talgent/dispatcher/public/v1"
	streamgatewaypublicv1 "github.com/talgent-work/workbench/gen/go/talgent/streamgateway/public/v1"
	"github.com/talgent-work/workbench/gen/go/talgent/streamgateway/public/v1/streamgatewaypublicv1connect"
	"github.com/talgent-work/workbench/internal/business/streamgateway/application"
	"github.com/talgent-work/workbench/internal/business/streamgateway/application/query"
	"github.com/talgent-work/workbench/pkg/authn"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Handler struct{ app *application.Application }

var _ streamgatewaypublicv1connect.StreamGatewayServiceHandler = (*Handler)(nil)

func NewHandler(app *application.Application) *Handler { return &Handler{app: app} }

func (h *Handler) WatchStream(ctx context.Context, req *connect.Request[streamgatewaypublicv1.WatchStreamRequest], srv *connect.ServerStream[streamgatewaypublicv1.WatchStreamResponse]) error {
	actor, _ := authn.CurrentUserID(ctx)
	after := time.Time{}
	if ts := req.Msg.GetAfterTimestamp(); ts != nil {
		after = ts.AsTime()
	}
	var kinds []string
	for _, kind := range req.Msg.GetKinds() {
		kinds = append(kinds, kind.String())
	}
	err := h.app.Queries.WatchStream.Run(ctx, query.WatchStreamRequest{
		ActorUserID: actor, Admission: readerAdmission(req.Header()), IncludeUnattributedErrors: req.Msg.GetIncludeUnattributedErrors(),
		AgentWorkID: req.Msg.GetAgentWorkId(), After: after, AfterID: req.Msg.GetAfterId(), Agent: req.Msg.Agent, Kinds: kinds, TurnIDs: req.Msg.GetTurnIds(),
	}, func(payload query.PortalLivePayload) error {
		response := mapPortalLivePayload(payload)
		if response == nil {
			return nil
		}
		return srv.Send(response)
	})
	return mapError(err)
}

func (h *Handler) ListMessagesBySession(ctx context.Context, req *connect.Request[streamgatewaypublicv1.ListMessagesBySessionRequest]) (*connect.Response[streamgatewaypublicv1.ListMessagesBySessionResponse], error) {
	actor, _ := authn.CurrentUserID(ctx)
	var before *query.MessageHistoryCursor
	if cursor := req.Msg.GetBefore(); cursor != nil {
		if cursor.Timestamp == nil || cursor.Timestamp.CheckValid() != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, query.ErrInvalidHistoryCursor)
		}
		before = &query.MessageHistoryCursor{Timestamp: cursor.Timestamp.AsTime(), ID: cursor.Id}
	}
	var from *query.MessageHistoryCursor
	if cursor := req.Msg.GetFrom(); cursor != nil {
		if cursor.Timestamp == nil || cursor.Timestamp.CheckValid() != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, query.ErrInvalidHistoryCursor)
		}
		from = &query.MessageHistoryCursor{Timestamp: cursor.Timestamp.AsTime(), ID: cursor.Id}
	}
	var kinds []string
	for _, kind := range req.Msg.GetKinds() {
		kinds = append(kinds, kind.String())
	}
	page, err := h.app.Queries.ListMessagesBySession.Handle(ctx, query.ListMessagesBySessionRequest{
		ActorUserID: actor, Admission: readerAdmission(req.Header()), IncludeUnattributedErrors: req.Msg.GetIncludeUnattributedErrors(),
		Before: before, From: from, Kinds: kinds, Agent: req.Msg.Agent, TurnIDs: req.Msg.GetTurnIds(), AgentWorkID: req.Msg.GetAgentWorkId(), ToolCallIDs: req.Msg.GetToolCallIds(), InteractionIDs: req.Msg.GetInteractionIds(), Limit: int(req.Msg.GetLimit()),
	})
	if err != nil {
		return nil, mapError(err)
	}
	response := &streamgatewaypublicv1.ListMessagesBySessionResponse{HasMore: page.HasMore}
	for _, message := range page.Messages {
		response.Messages = append(response.Messages, messageToProto(message))
	}
	if page.NextBefore != nil {
		response.NextBefore = &streamgatewaypublicv1.MessageHistoryCursor{Timestamp: timestamppb.New(page.NextBefore.Timestamp), Id: page.NextBefore.ID}
	}
	return connect.NewResponse(response), nil
}

func (h *Handler) GetMessageDetail(ctx context.Context, req *connect.Request[streamgatewaypublicv1.GetMessageDetailRequest]) (*connect.Response[streamgatewaypublicv1.GetMessageDetailResponse], error) {
	actor, _ := authn.CurrentUserID(ctx)
	detail, err := h.app.GetMessageDetail(ctx, query.MessageDetailRequest{
		ActorUserID: actor, Admission: readerAdmission(req.Header()), AgentWorkID: req.Msg.AgentWorkId, MessageID: req.Msg.MessageId, Offset: req.Msg.Offset, Limit: req.Msg.Limit,
	})
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(detailResponse(&detail)), nil
}

func (h *Handler) GetToolDetail(ctx context.Context, req *connect.Request[streamgatewaypublicv1.GetToolDetailRequest]) (*connect.Response[streamgatewaypublicv1.GetToolDetailResponse], error) {
	actor, _ := authn.CurrentUserID(ctx)
	detail, err := h.app.GetToolDetail(ctx, query.ToolDetailRequest{ActorUserID: actor, Admission: readerAdmission(req.Header()), AgentWorkID: req.Msg.AgentWorkId, ToolCallID: req.Msg.ToolCallId, InputOffset: req.Msg.InputOffset, OutputOffset: req.Msg.OutputOffset, Limit: req.Msg.Limit})
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&streamgatewaypublicv1.GetToolDetailResponse{Input: detailResponse(detail.Input), Output: detailResponse(detail.Output), Preview: toolPreviewResponse(detail.Preview)}), nil
}

// readerAdmission reads a trusted delegated caller's admission of the reader;
// a client's own request never carries one.
func readerAdmission(header http.Header) query.ReaderAdmission {
	reference, value, ok := authn.ReaderAdmission(header)
	if !ok {
		return query.ReaderAdmission{}
	}
	return query.ReaderAdmission{Reference: reference, Value: value}
}

func detailResponse(detail *query.MessageDetail) *streamgatewaypublicv1.GetMessageDetailResponse {
	if detail == nil {
		return nil
	}
	return &streamgatewaypublicv1.GetMessageDetailResponse{Format: dispatcherpublicv1.MessageDetailFormat(detail.Format), Text: detail.Text, TotalLength: detail.TotalLength, NextOffset: detail.NextOffset}
}

func mapError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, query.ErrSessionIDRequired), errors.Is(err, query.ErrInvalidHistoryCursor), errors.Is(err, query.ErrInvalidDetailRequest):
		return connect.NewError(connect.CodeInvalidArgument, err)
	case errors.Is(err, query.ErrWorkAccessDenied):
		return connect.NewError(connect.CodePermissionDenied, err)
	case errors.Is(err, query.ErrMessageNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, query.ErrLiveUnavailable), errors.Is(err, query.ErrWorkAccessUnavailable):
		return connect.NewError(connect.CodeUnavailable, err)
	default:
		return connect.NewError(connect.CodeInternal, err)
	}
}

func toolPreviewResponse(preview *query.ToolOutputPreview) *dispatcherpublicv1.ToolOutputPreview {
	if preview == nil {
		return nil
	}
	message := &dispatcherpublicv1.ToolOutputPreview{
		ToolName: preview.ToolName, FilePath: preview.FilePath, Content: preview.Content,
		StartLine: preview.StartLine, NumLines: preview.NumLines, TotalLines: preview.TotalLines,
		TruncatedByTokenCap: preview.TruncatedByTokenCap, Truncated: preview.Truncated, TotalLength: preview.TotalLength,
	}
	for _, hunk := range preview.Hunks {
		message.Hunks = append(message.Hunks, &dispatcherpublicv1.PatchHunk{
			OldStart: hunk.OldStart, OldLines: hunk.OldLines, NewStart: hunk.NewStart, NewLines: hunk.NewLines, Lines: hunk.Lines,
		})
	}
	return message
}
