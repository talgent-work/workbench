package query

import (
	"context"
	"time"
)

type ReplayCursor struct {
	IncludeUnattributedErrors bool
	Agent                     *string
	Kinds                     []string
	TurnIDs                   []string
	After                     time.Time
	AfterID                   string
	Limit                     int
	Latest                    bool
}

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
	ActorUserID               string
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

// UnifiedMessageReader queries the durable message log for Streamgateway
// replay and message lookup use cases.
type UnifiedMessageReader interface {
	GetToolDetail(context.Context, ToolDetailRequest) (ToolDetail, error)
	GetMessageDetail(context.Context, MessageDetailRequest) (MessageDetail, error)
	ListReplay(ctx context.Context, sessionID string, cursor ReplayCursor) ([]*Message, error)
	ListMessagesBySession(ctx context.Context, req ListMessagesBySessionRequest) ([]*Message, error)
}
