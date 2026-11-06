package domain

import "context"

// ComputerRepository is the collection of Computers. Save fails with
// ErrConcurrentModification when the Computer changed since it was loaded.
type ComputerRepository interface {
	Get(ctx context.Context, id string) (*Computer, error)
	Save(ctx context.Context, computer *Computer) error
	// FindByCredential finds the Managed Computer whose current, successor or
	// replaced Computer Credential has this digest, or fails with
	// ErrComputerNotFound.
	FindByCredential(ctx context.Context, digest string) (*Computer, error)
}

// DeskRepository is the collection of Desks. Saving a new Desk whose
// identity is taken fails with ErrDeskTaken.
type DeskRepository interface {
	Get(ctx context.Context, id string) (*Desk, error)
	Save(ctx context.Context, desk *Desk) error
	// ListOpen lists the waiting and occupied Desks on a Computer.
	ListOpen(ctx context.Context, computerID string) ([]*Desk, error)
}
