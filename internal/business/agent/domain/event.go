package domain

import (
	"time"

	domainevent "github.com/go-jimu/components/ddd/event"
)

type EventKind = domainevent.Kind
type Event = domainevent.Event
type EventCollection = domainevent.Collection

func NewEventCollection() EventCollection { return domainevent.NewCollection() }

const (
	EventKindWorkShareRevoked EventKind = "agent.work.share_revoked"
	EventKindWorkMadePrivate  EventKind = "agent.work.made_private"
	EventKindExecutionEnded   EventKind = "agent.work.execution_ended"
	EventKindClosing          EventKind = "agent.work.closing"
	EventKindClosed           EventKind = "agent.work.closed"
)

// WorkExecutionEnded records that a Work stopped or failed. A close request
// the Work kept while it was running can apply now.
type WorkExecutionEnded struct {
	WorkID string
}

func (WorkExecutionEnded) Kind() EventKind { return EventKindExecutionEnded }
func (e WorkExecutionEnded) Key() string   { return e.WorkID }

// WorkClosingEntered records that a Work entered Closing; its input buffer is
// to be emptied (Closure admission order).
type WorkClosingEntered struct {
	WorkID string
}

func (WorkClosingEntered) Kind() EventKind { return EventKindClosing }
func (e WorkClosingEntered) Key() string   { return e.WorkID }

// WorkClosedEvent is the WorkClosed Domain Event: a Work reached Closed and
// no Harness process remains, so Storage is asked once to reclaim a Cloud
// Work's Work Directories (Request Work reclamation). The type is named
// apart from the WorkClosed state.
type WorkClosedEvent struct {
	WorkID string
	// Cloud reports a Work on a Cloud, which mounted Work Directories.
	Cloud bool
}

func (WorkClosedEvent) Kind() EventKind { return EventKindClosed }
func (e WorkClosedEvent) Key() string   { return e.WorkID }

// WorkShareRevoked records that a reader no longer holds a Work Share; that
// reader's open message streams of the Work end immediately.
type WorkShareRevoked struct {
	WorkID     string
	ReaderID   string
	OccurredAt time.Time
}

func (WorkShareRevoked) Kind() EventKind { return EventKindWorkShareRevoked }
func (e WorkShareRevoked) Key() string   { return e.WorkID }

// WorkMadePrivate records that a Work became private. Open message streams of
// readers other than its Owners and its Work Share holders end immediately.
type WorkMadePrivate struct {
	WorkID         string
	Owners         []string
	ShareReaderIDs []string
	OccurredAt     time.Time
}

func (WorkMadePrivate) Kind() EventKind { return EventKindWorkMadePrivate }
func (e WorkMadePrivate) Key() string   { return e.WorkID }
