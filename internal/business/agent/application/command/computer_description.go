package command

import (
	"context"
	"time"
)

// ComputerDescription is what Computer says a Work's Computer is.
type ComputerDescription struct {
	// Managed is a person's machine reached through Computer Gateway; a
	// Computer that is not Managed is a Cloud.
	Managed bool
	// Name is its current name, as people see it.
	Name string
	// Removed means its owner removed it, which confirms the loss of every
	// Work's execution environment on it.
	Removed bool
	// Reachable is its Presence: whether the platform reaches it now.
	// UnreachableSince is when the platform lost reach of it, while it does
	// not.
	Reachable        bool
	UnreachableSince time.Time
}

// ComputerDescriptionPort is Computer's description contract as Agent uses
// it, served by the same Computer client as ComputerAdmissionPort. An
// unknown Computer is one the Work cannot use.
type ComputerDescriptionPort interface {
	DescribeComputer(ctx context.Context, computerID string) (ComputerDescription, error)
}
