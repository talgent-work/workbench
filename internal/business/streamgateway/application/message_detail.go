package application

import (
	"context"
	"strings"

	"github.com/talgent-work/workbench/internal/business/streamgateway/application/query"
)

func (a *Application) GetMessageDetail(ctx context.Context, request query.MessageDetailRequest) (query.MessageDetail, error) {
	request.AgentWorkID, request.MessageID = strings.TrimSpace(request.AgentWorkID), strings.TrimSpace(request.MessageID)
	if request.AgentWorkID == "" || request.MessageID == "" {
		return query.MessageDetail{}, query.ErrInvalidDetailRequest
	}
	if err := a.access.AuthorizeRead(ctx, request.ActorUserID, request.Admission, request.AgentWorkID); err != nil {
		return query.MessageDetail{}, err
	}
	request.Limit = detailPageLimit(request.Limit)
	return a.reader.GetMessageDetail(ctx, request)
}

func (a *Application) GetToolDetail(ctx context.Context, request query.ToolDetailRequest) (query.ToolDetail, error) {
	request.AgentWorkID, request.ToolCallID = strings.TrimSpace(request.AgentWorkID), strings.TrimSpace(request.ToolCallID)
	if request.AgentWorkID == "" || request.ToolCallID == "" {
		return query.ToolDetail{}, query.ErrInvalidDetailRequest
	}
	if err := a.access.AuthorizeRead(ctx, request.ActorUserID, request.Admission, request.AgentWorkID); err != nil {
		return query.ToolDetail{}, err
	}
	request.Limit = detailPageLimit(request.Limit)
	detail, err := a.reader.GetToolDetail(ctx, request)
	if err != nil {
		return query.ToolDetail{}, err
	}
	// A preview belongs to the call only when its shape is the called
	// tool's; otherwise the original output alone is shown. It comes with the
	// first page only, so paging the original detail never resends it.
	if detail.Preview != nil && (detail.Preview.ToolName != detail.ToolName || request.InputOffset != 0 || request.OutputOffset != 0) {
		detail.Preview = nil
	}
	return detail, nil
}

func detailPageLimit(limit uint32) uint32 {
	if limit == 0 {
		return 16 * 1024
	}
	return min(limit, 64*1024)
}
