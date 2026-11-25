package query

import (
	"context"
	"time"
)

type MessageHistoryCursor struct {
	Timestamp time.Time
	ID        string
}

type MessageHistoryPage struct {
	Messages   []*Message
	NextBefore *MessageHistoryCursor
	HasMore    bool
}

type ListMessagesBySessionRequest struct {
	IncludeUnattributedErrors bool
	ReaderID                  string
	Admission                 ReaderAdmission
	Before                    *MessageHistoryCursor
	From                      *MessageHistoryCursor
	AgentWorkID               string
	Kinds                     []string
	Agent                     *string
	TurnIDs                   []string
	ToolCallIDs               []string
	InteractionIDs            []string
	Limit                     int
}

// UnifiedMessageReader queries the durable message log for history and
// message lookups.
type UnifiedMessageReader interface {
	GetToolDetail(context.Context, ToolDetailRequest) (ToolDetail, error)
	GetMessageDetail(context.Context, MessageDetailRequest) (MessageDetail, error)
	ListMessagesBySession(ctx context.Context, req ListMessagesBySessionRequest) ([]*Message, error)
}
