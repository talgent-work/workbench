package query

import (
	"context"
	"errors"
	"slices"
	"time"
)

// PortalLiveStream subscribes to a Work's live frames. Live delivery is
// best-effort: frames may be lost, repeated or reordered.
type PortalLiveStream interface {
	// Subscribe hands each of the Work's live frames to deliver until the
	// subscription closes or ends. deliver must not block, and it may be called
	// from more than one goroutine. ctx bounds only the subscribing itself.
	Subscribe(ctx context.Context, workID string, deliver func(PortalLivePayload)) (PortalLiveSubscription, error)
}

// PortalLiveSubscription is one Work's live subscription.
type PortalLiveSubscription interface {
	// Ended is closed once the subscription ends on its own, when its
	// connection is closed for good.
	Ended() <-chan struct{}
	Close() error
}

// ErrInvalidWorkID rejects a Work identity that cannot name one Work's frames.
var ErrInvalidWorkID = errors.New("invalid agent_work_id")

// PortalLivePayload is a live frame Streamgateway can forward to Portal.
type PortalLivePayload interface {
	isPortalLivePayload()
}

type UnifiedMessagePayload struct {
	Message *Message
}

func (*UnifiedMessagePayload) isPortalLivePayload() {}

type WorkStatePayload struct {
	Frame *WorkState
}

func (*WorkStatePayload) isPortalLivePayload() {}

type DiagnosticPayload struct {
	Frame *Diagnostic
}

func (*DiagnosticPayload) isPortalLivePayload() {}

// HeartbeatPayload tells a quiet stream's reader that the stream is alive.
type HeartbeatPayload struct{}

func (*HeartbeatPayload) isPortalLivePayload() {}

// ErrWorkAccessWithdrawn ends a stream whose reader lost access to the Work.
var ErrWorkAccessWithdrawn = errors.New("work access withdrawn")

// WorkAccessPayload is Agent's notice that readers lost access to the Work's
// message stream: one revoked reader, or, once the Work became private, every
// reader but the retained ones. It is never sent to a client.
type WorkAccessPayload struct {
	RevokedReaderID   string
	MadePrivate       bool
	RetainedReaderIDs []string
}

func (*WorkAccessPayload) isPortalLivePayload() {}

// Withdraws reports whether the notice ends this reader's access.
func (p *WorkAccessPayload) Withdraws(reader string) bool {
	if p.MadePrivate {
		return !slices.Contains(p.RetainedReaderIDs, reader)
	}
	return p.RevokedReaderID != "" && p.RevokedReaderID == reader
}

// AccessWithdrawnPayload is the last frame a reader receives after losing
// access, distinct from a connection error.
type AccessWithdrawnPayload struct{ AgentWorkID string }

func (*AccessWithdrawnPayload) isPortalLivePayload() {}

// WorkState is the existing Work presentation, attributed to its actual Session.
// Optional times and progress preserve absence rather than inventing a snapshot.
type StartupProgress struct {
	RequestID, Revision, Status, Reason string
	Admitted                            bool
	Stages                              []StartupStage
}
type StartupStage struct{ Stage, Status string }

type WorkState struct {
	PreparationProgress *StartupProgress
	AgentWorkID         string
	State               string
	StateReason         string
	UpdatedAt           *time.Time
	WorkProgress        *WorkProgress
	// SubAgents of the Work's current Harness process, in delegation order.
	SubAgents []SubAgent
}
type SubAgent struct{ ID, Type, Task, Status string }
type WorkProgress struct {
	Items     []WorkProgressItem
	UpdatedAt *time.Time
}
type WorkProgressItem struct {
	ID         string
	Content    string
	Status     string
	ActiveForm string
}
type Diagnostic struct {
	AgentWorkID string
	Severity    string
	Reason      string
	Message     string
	OccurredAt  *time.Time
}
