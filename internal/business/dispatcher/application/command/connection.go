package command

import (
	"context"
	"errors"
)

var ErrNoActiveConnection = errors.New("dispatcher: no active native execution connection")

var ErrInvalidUpdatedInput = errors.New("dispatcher: invalid updated input JSON")

// ExecutionOwner is the replica reading a Work's Harness Output Stream. Alive
// reports whether that replica still refreshes its liveness.
type ExecutionOwner struct {
	Instance, Addr string
	Alive          bool
}

// ExecutionOwners locates the replica reading a Work's stream, so a command
// reaches the one replica that can act on it.
type ExecutionOwners interface {
	Locate(ctx context.Context, workID string) (ExecutionOwner, bool, error)
}
