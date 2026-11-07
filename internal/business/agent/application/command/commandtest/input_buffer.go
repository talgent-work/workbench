// Package commandtest provides test doubles for Agent command ports.
package commandtest

import (
	"context"
	"slices"
	"sync"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
)

// InputBuffer stands in for Dispatcher's input buffer of one Work: it
// deduplicates by identity, remembers settled identities and reports what
// awaits processing. The Redis buffer is tested at its own boundary. It
// stands in for no other part of the running Harness: a test that reaches one
// fails loudly on the nil port it embeds.
type InputBuffer struct {
	command.RunningHarnessPort
	mu      sync.Mutex
	Inputs  []command.ExecutionInput
	Replies []command.InteractionResponse
	Dropped int
	Err     error
	settled map[identity]bool
}

var _ command.RunningHarnessPort = (*InputBuffer)(nil)

// identity names a buffer entry: an input, or the interaction a reply answered.
type identity struct {
	ID    string
	Reply bool
}

func (b *InputBuffer) BufferInput(_ context.Context, _ string, input command.ExecutionInput) (command.InputHandOff, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if handOff, done := b.handOff(identity{ID: input.ID}, slices.ContainsFunc(b.Inputs, func(buffered command.ExecutionInput) bool { return buffered.ID == input.ID })); done {
		return handOff, b.Err
	}
	b.Inputs = append(b.Inputs, input)
	return command.InputBuffered, nil
}

func (b *InputBuffer) BufferResponse(_ context.Context, _ string, response command.InteractionResponse) (command.InputHandOff, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if handOff, done := b.handOff(identity{ID: response.InteractionID, Reply: true}, slices.ContainsFunc(b.Replies, func(buffered command.InteractionResponse) bool {
		return buffered.InteractionID == response.InteractionID
	})); done {
		return handOff, b.Err
	}
	b.Replies = append(b.Replies, response)
	return command.InputBuffered, nil
}

// handOff answers a hand-off that appends nothing: a failure, a settled
// identity or a buffered one.
func (b *InputBuffer) handOff(entry identity, buffered bool) (command.InputHandOff, bool) {
	switch {
	case b.Err != nil:
		return "", true
	case b.settled[entry]:
		return command.InputAlreadySettled, true
	case buffered:
		return command.InputRepeated, true
	}
	return "", false
}

func (b *InputBuffer) ReadBuffer(context.Context, string) (command.BufferedInputs, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.Err != nil {
		return command.BufferedInputs{}, b.Err
	}
	var result command.BufferedInputs
	for _, input := range b.Inputs {
		result.InputIDs = append(result.InputIDs, input.ID)
	}
	for _, reply := range b.Replies {
		result.RepliedInteractionIDs = append(result.RepliedInteractionIDs, reply.InteractionID)
	}
	return result, nil
}

func (b *InputBuffer) DropBuffer(context.Context, string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.Inputs, b.Replies = nil, nil
	b.Dropped++
	return nil
}

// Settle removes an entry as Dispatcher does once the Harness settles it.
func (b *InputBuffer) Settle(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.settled == nil {
		b.settled = map[identity]bool{}
	}
	if slices.ContainsFunc(b.Inputs, func(input command.ExecutionInput) bool { return input.ID == id }) {
		b.settled[identity{ID: id}] = true
	}
	if slices.ContainsFunc(b.Replies, func(reply command.InteractionResponse) bool { return reply.InteractionID == id }) {
		b.settled[identity{ID: id, Reply: true}] = true
	}
	b.Inputs = slices.DeleteFunc(b.Inputs, func(input command.ExecutionInput) bool { return input.ID == id })
	b.Replies = slices.DeleteFunc(b.Replies, func(reply command.InteractionResponse) bool { return reply.InteractionID == id })
}
