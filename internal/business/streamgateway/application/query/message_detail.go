package query

import (
	"context"
	"errors"
)

var (
	ErrWorkAccessDenied      = errors.New("Work messages are not accessible")
	ErrWorkAccessUnavailable = errors.New("Work authorization is unavailable")
	ErrMessageNotFound       = errors.New("message is not available in stored history")
	ErrInvalidDetailRequest  = errors.New("detail requires a Work and message or tool identity")
)

// ReaderAdmission is a trusted internal caller's statement that the reader is
// a member of a Work Caller, by its kind and identity, such as a Project.
// Agent decides whether that Work Caller is the Work's. Its zero value admits
// nothing; a client can never supply it.
type ReaderAdmission struct{ WorkCallerKind, WorkCallerID string }

// WorkAccessPort admits a reader of a Work's message stream from Agent's own
// facts: the owner, a Work Share holder, or a reader admitted within the scope
// of a public Work. A reader is the id of the reading person or Application;
// StreamGateway never tells them apart, Agent decides.
type WorkAccessPort interface {
	AuthorizeRead(ctx context.Context, reader string, admission ReaderAdmission, workID string) error
}

// StreamAccessPort admits a stream's reader, also answering the Work's current
// state, the frame the stream starts from; nil when Agent supplies none.
type StreamAccessPort interface {
	WorkAccessPort
	AdmitStreamReader(ctx context.Context, reader string, admission ReaderAdmission, workID string) (*WorkState, error)
}

type MessageDetailRequest struct {
	ReaderID, AgentWorkID, MessageID string
	Admission                        ReaderAdmission
	Offset, Limit                    uint32
}

// Offsets and lengths count Unicode code points, not bytes. Format zero means
// this stored message has no detail; a nonzero format can have an empty body.
type MessageDetail struct {
	Format                  uint8
	Text                    string
	TotalLength, NextOffset uint32
}

// ToolDetail returns the recorded call and its latest result in one read.
// A nil side is unavailable, distinct from a recorded empty body.
type ToolDetailRequest struct {
	ReaderID, AgentWorkID, ToolCallID string
	Admission                         ReaderAdmission
	InputOffset, OutputOffset, Limit  uint32
}

type ToolDetail struct {
	Input, Output *MessageDetail
	// ToolName is the tool the recorded call named.
	ToolName string
	// Preview is what the result's own structured output showed; nil when
	// it had none.
	Preview *ToolOutputPreview
}

// ToolOutputPreview is the previewable part of a Read, Write or Edit result.
type ToolOutputPreview struct {
	// ToolName is the tool whose result shape this is.
	ToolName                        string
	FilePath, Content               string
	StartLine, NumLines, TotalLines uint32
	TruncatedByTokenCap, Truncated  bool
	TotalLength                     uint32
	Hunks                           []PatchHunk
}

// PatchHunk is one unified-diff hunk of an Edit.
type PatchHunk struct {
	OldStart, OldLines, NewStart, NewLines uint32
	Lines                                  []string
}
