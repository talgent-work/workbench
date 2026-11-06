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
	EventKindDeskAssigned EventKind = "computer.desk.assigned"
	EventKindDeskOccupied EventKind = "computer.desk.occupied"
	EventKindDeskVacated  EventKind = "computer.desk.vacated"

	EventKindComputerBecameUnreachable EventKind = "computer.became_unreachable"
	EventKindComputerBecameReachable   EventKind = "computer.became_reachable"
	EventKindComputerRemoved           EventKind = "computer.removed"
)

// ComputerRemoved records that its owner removed a Managed Computer: its
// credential is revoked for good, every Desk on it leaves, and the
// execution environment of every Work on it is lost.
type ComputerRemoved struct {
	ComputerID string
	At         time.Time
}

func (ComputerRemoved) Kind() EventKind { return EventKindComputerRemoved }
func (e ComputerRemoved) Key() string   { return e.ComputerID }

// ComputerBecameUnreachable records that the platform lost reach of a
// Managed Computer.
type ComputerBecameUnreachable struct {
	ComputerID string
	Since      time.Time
}

func (ComputerBecameUnreachable) Kind() EventKind { return EventKindComputerBecameUnreachable }
func (e ComputerBecameUnreachable) Key() string   { return e.ComputerID }

// ComputerBecameReachable records that the platform reaches a Managed
// Computer again.
type ComputerBecameReachable struct {
	ComputerID string
}

func (ComputerBecameReachable) Kind() EventKind { return EventKindComputerBecameReachable }
func (e ComputerBecameReachable) Key() string   { return e.ComputerID }

// DeskAssigned records that a Computer assigned room to the head of its
// Waiting Line; that Desk seats the run it was waiting for.
type DeskAssigned struct {
	ComputerID string
	DeskID     string
}

func (DeskAssigned) Kind() EventKind { return EventKindDeskAssigned }
func (e DeskAssigned) Key() string   { return e.DeskID }

// DeskOccupied records that a Desk holds room and its run may proceed.
type DeskOccupied struct {
	DeskID string
	WorkID string
}

func (DeskOccupied) Kind() EventKind { return EventKindDeskOccupied }
func (e DeskOccupied) Key() string   { return e.DeskID }

// DeskVacated records that a Desk left; its Computer frees its room or its
// place in line.
type DeskVacated struct {
	DeskID     string
	ComputerID string
	WorkID     string
	Demand     Room
	// Seated reports that the Desk held room when it left, rather than
	// waiting in line.
	Seated bool
}

func (DeskVacated) Kind() EventKind { return EventKindDeskVacated }
func (e DeskVacated) Key() string   { return e.DeskID }
