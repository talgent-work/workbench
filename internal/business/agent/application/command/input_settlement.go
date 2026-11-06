package command

import (
	"context"
	"time"
)

// InputOutcome is how an input a caller delivered settled.
type InputOutcome string

const (
	// InputCompleted: a Turn processed it.
	InputCompleted InputOutcome = "completed"
	// InputAbandoned: the process ended before finishing it.
	InputAbandoned InputOutcome = "abandoned"
	// InputDropped: the Work closed before any Turn processed it.
	InputDropped InputOutcome = "dropped"
)

// InputSettlement is that one delivered input settled, named by the
// caller's delivery identity.
type InputSettlement struct {
	WorkID  string
	InputID string
	Outcome InputOutcome
	At      time.Time
}

// InputSettlements reports Platform Input Settlement to the caller that
// delivered the input. Agent keeps no delivery record, so every
// accepted input that settles is reported and each caller recognizes its
// own; an input refused at acceptance is not, since its caller was refused.
// A report that fails is returned, so the feedback carrying it is retried.
type InputSettlements interface {
	Settled(ctx context.Context, settlement InputSettlement) error
}
