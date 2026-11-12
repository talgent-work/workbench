package query

import (
	"context"
	"errors"
	"strings"
)

var ErrInvalidHistoryCursor = errors.New("specify at most one history cursor, with a timestamp and id")

var ErrSessionIDRequired = errors.New("agent_work_id is required")

type ListMessagesBySessionHandler struct {
	reader UnifiedMessageReader
	access WorkAccessPort
}

func NewListMessagesBySessionHandler(reader UnifiedMessageReader, access WorkAccessPort) *ListMessagesBySessionHandler {
	return &ListMessagesBySessionHandler{reader: reader, access: access}
}

func (h *ListMessagesBySessionHandler) Handle(ctx context.Context, req ListMessagesBySessionRequest) (*MessageHistoryPage, error) {
	if req.AgentWorkID == "" {
		return nil, ErrSessionIDRequired
	}
	if err := h.access.AuthorizeRead(ctx, req.ReaderID, req.Admission, req.AgentWorkID); err != nil {
		return nil, err
	}
	req.Kinds = normalizeMessageFilters(req.Kinds)
	req.TurnIDs = normalizeMessageFilters(req.TurnIDs)
	req.ToolCallIDs = normalizeMessageFilters(req.ToolCallIDs)
	req.InteractionIDs = normalizeMessageFilters(req.InteractionIDs)
	if req.Limit <= 0 || req.Limit > 500 {
		req.Limit = 500
	}
	if (req.Before != nil && req.From != nil) || (req.Before != nil && (req.Before.Timestamp.IsZero() || req.Before.ID == "")) || (req.From != nil && (req.From.Timestamp.IsZero() || req.From.ID == "")) {
		return nil, ErrInvalidHistoryCursor
	}
	limit := req.Limit
	req.Limit++
	rows, err := h.reader.ListMessagesBySession(ctx, req)
	if err != nil {
		return nil, err
	}
	page := &MessageHistoryPage{Messages: rows}
	page.HasMore = len(rows) > limit
	if page.HasMore {
		if req.From != nil {
			page.Messages = rows[:limit]
		} else {
			page.Messages = rows[len(rows)-limit:]
		}
	}
	if len(page.Messages) > 0 {
		oldest := page.Messages[0]
		page.NextBefore = &MessageHistoryCursor{Timestamp: oldest.Timestamp, ID: oldest.ID}
	}

	return page, nil
}

func normalizeMessageFilters(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		out = append(out, value)
	}
	return out
}
