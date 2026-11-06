package query

import (
	"context"
	"errors"
	"slices"
	"time"
)

// PortalLiveStream is Streamgateway's single live query port for Portal frames.
type PortalLiveStream interface {
	SubscribeSession(ctx context.Context, sessionID string) (PortalLiveSubscription, error)
}

// PortalLiveSubscription is a per-Session live stream handle.
type PortalLiveSubscription interface {
	Recv(ctx context.Context) (PortalLivePayload, error)
	Close() error
}

// PortalLivePayload is a live frame Streamgateway can forward to Portal.
type PortalLivePayload interface {
	isPortalLivePayload()
}

type UnifiedMessagePayload struct {
	Message *Message
	Replay  bool
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
