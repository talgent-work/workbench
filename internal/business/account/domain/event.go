package domain

import (
	"time"

	domainevent "github.com/go-jimu/components/ddd/event"
)

type EventKind = domainevent.Kind
type Event = domainevent.Event
type EventCollection = domainevent.Collection

func NewEventCollection() EventCollection { return domainevent.NewCollection() }

const EventKindApplicationRemoved EventKind = "account.application.removed"

// ApplicationRemovedEvent records that its Application Owner removed an
// Application for good (ApplicationRemoved); Account publishes it as the
// Application Removed Published Fact.
type ApplicationRemovedEvent struct {
	ApplicationID ApplicationID
	OccurredAt    time.Time
}

func (ApplicationRemovedEvent) Kind() EventKind { return EventKindApplicationRemoved }
func (e ApplicationRemovedEvent) Key() string   { return string(e.ApplicationID) }
