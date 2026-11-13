package connectrpc

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect/v2"
	dispatcherpublicv1 "github.com/talgent-work/contracts/gen/go/dispatcher/public/v1"
	streamgatewaypublicv1 "github.com/talgent-work/contracts/gen/go/streamgateway/public/v1"
	"github.com/talgent-work/contracts/gen/go/streamgateway/public/v1/streamgatewaypublicv1connect"
	"github.com/talgent-work/workbench/internal/business/streamgateway/application"
	"github.com/talgent-work/workbench/internal/business/streamgateway/application/query"
	"github.com/talgent-work/workbench/internal/pkg/authn"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Handler struct{ app *application.Application }

var _ streamgatewaypublicv1connect.StreamGatewayServiceHandler = (*Handler)(nil)

func NewHandler(app *application.Application) *Handler { return &Handler{app: app} }

func (h *Handler) WatchStream(ctx context.Context, req *streamgatewaypublicv1.WatchStreamRequest, srv streamgatewaypublicv1connect.StreamGatewayServiceWatchStreamServerStream) error {
	info, _ := connect.CallInfoForServerContext(ctx)
	reader := streamReader(ctx)
	after := time.Time{}
	if ts := req.GetAfterTimestamp(); ts != nil {
		after = ts.AsTime()
	}
	var kinds []string
	for _, kind := range req.GetKinds() {
		kinds = append(kinds, kind.String())
	}
	err := h.app.Queries.WatchStream.Run(ctx, query.WatchStreamRequest{
		ReaderID: reader, Admission: readerAdmission(info.RequestHeader()), IncludeUnattributedErrors: req.GetIncludeUnattributedErrors(),
		AgentWorkID: req.GetAgentWorkId(), After: after, AfterID: req.GetAfterId(), Agent: req.Agent, Kinds: kinds, TurnIDs: req.GetTurnIds(),
	}, func(payload query.PortalLivePayload) error {
		response := mapPortalLivePayload(payload)
		if response == nil {
			return nil
		}
		return srv.Send(response)
	})
	return mapError(err)
}

func (h *Handler) ListMessagesBySession(ctx context.Context, req *streamgatewaypublicv1.ListMessagesBySessionRequest) (*streamgatewaypublicv1.ListMessagesBySessionResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	reader := streamReader(ctx)
	var before *query.MessageHistoryCursor
	if cursor := req.GetBefore(); cursor != nil {
		if cursor.Timestamp == nil || cursor.Timestamp.CheckValid() != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, query.ErrInvalidHistoryCursor.Error()).WithCause(query.ErrInvalidHistoryCursor)
		}
		before = &query.MessageHistoryCursor{Timestamp: cursor.Timestamp.AsTime(), ID: cursor.Id}
	}
	var from *query.MessageHistoryCursor
	if cursor := req.GetFrom(); cursor != nil {
		if cursor.Timestamp == nil || cursor.Timestamp.CheckValid() != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, query.ErrInvalidHistoryCursor.Error()).WithCause(query.ErrInvalidHistoryCursor)
		}
		from = &query.MessageHistoryCursor{Timestamp: cursor.Timestamp.AsTime(), ID: cursor.Id}
	}
	var kinds []string
	for _, kind := range req.GetKinds() {
		kinds = append(kinds, kind.String())
	}
	page, err := h.app.Queries.ListMessagesBySession.Handle(ctx, query.ListMessagesBySessionRequest{
		ReaderID: reader, Admission: readerAdmission(info.RequestHeader()), IncludeUnattributedErrors: req.GetIncludeUnattributedErrors(),
		Before: before, From: from, Kinds: kinds, Agent: req.Agent, TurnIDs: req.GetTurnIds(), AgentWorkID: req.GetAgentWorkId(), ToolCallIDs: req.GetToolCallIds(), InteractionIDs: req.GetInteractionIds(), Limit: int(req.GetLimit()),
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
	return response, nil
}

func (h *Handler) GetMessageDetail(ctx context.Context, req *streamgatewaypublicv1.GetMessageDetailRequest) (*streamgatewaypublicv1.GetMessageDetailResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	reader := streamReader(ctx)
	detail, err := h.app.GetMessageDetail(ctx, query.MessageDetailRequest{
		ReaderID: reader, Admission: readerAdmission(info.RequestHeader()), AgentWorkID: req.AgentWorkId, MessageID: req.MessageId, Offset: req.Offset, Limit: req.Limit,
	})
	if err != nil {
		return nil, mapError(err)
	}
	return detailResponse(&detail), nil
}

func (h *Handler) GetToolDetail(ctx context.Context, req *streamgatewaypublicv1.GetToolDetailRequest) (*streamgatewaypublicv1.GetToolDetailResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	reader := streamReader(ctx)
	detail, err := h.app.GetToolDetail(ctx, query.ToolDetailRequest{ReaderID: reader, Admission: readerAdmission(info.RequestHeader()), AgentWorkID: req.AgentWorkId, ToolCallID: req.ToolCallId, InputOffset: req.InputOffset, OutputOffset: req.OutputOffset, Limit: req.Limit})
	if err != nil {
		return nil, mapError(err)
	}
	return &streamgatewaypublicv1.GetToolDetailResponse{Input: detailResponse(detail.Input), Output: detailResponse(detail.Output), Preview: toolPreviewResponse(detail.Preview)}, nil
}

// readerAdmission reads a trusted delegated caller's admission of the reader;
// a client's own request never carries one.
func readerAdmission(header *connect.Header) query.ReaderAdmission {
	kind, id, ok := authn.ReaderAdmission(header)
	if !ok {
		return query.ReaderAdmission{}
	}
	return query.ReaderAdmission{WorkCallerKind: kind, WorkCallerID: id}
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
		return connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	case errors.Is(err, query.ErrWorkAccessDenied):
		return connect.NewError(connect.CodePermissionDenied, err.Error()).WithCause(err)
	case errors.Is(err, query.ErrMessageNotFound):
		return connect.NewError(connect.CodeNotFound, err.Error()).WithCause(err)
	case errors.Is(err, query.ErrLiveUnavailable), errors.Is(err, query.ErrWorkAccessUnavailable):
		return connect.NewError(connect.CodeUnavailable, err.Error()).WithCause(err)
	default:
		return connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
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

// streamReader is whom a read of a Work's message stream is for: the person
// or the Application the request speaks for. Agent admits each to its own
// Works and a person also by Work Share or Visibility.
func streamReader(ctx context.Context) string {
	if application, ok := authn.CurrentApplication(ctx); ok {
		return application.ID
	}
	user, _ := authn.CurrentUserID(ctx)
	return user
}
