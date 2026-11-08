//go:build integration

package infrastructure_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
)

// closingBuffer commits a permanent close while an input is being handed
// off, after acceptance read the Work and before it commits.
type closingBuffer struct {
	*commandtest.InputBuffer
	during func()
}

func (b closingBuffer) BufferInput(ctx context.Context, workID string, input command.ExecutionInput) (command.InputHandOff, error) {
	handOff, err := b.InputBuffer.BufferInput(ctx, workID, input)
	if b.during != nil {
		b.during()
	}
	return handOff, err
}

// An input handed off while a close stops the Work is accepted.
func TestInputAcceptanceAndPermanentCloseShareOneOrder(t *testing.T) {
	ctx := context.Background()
	f, ref := workingSession(t)
	buffer := closingBuffer{InputBuffer: &commandtest.InputBuffer{}}
	// The close read the buffer before the input was appended, so it saw
	// nothing pending, and its stop commits after the append.
	closeSession := command.NewCloseWorkHandler(f.sessions, access.NewWorkAuthorizer(infrastructure.NewWorkQueryRepository(f.db).WorkOwners), executionsOf(f.db), &commandtest.InputBuffer{})
	buffer.during = func() {
		require.NoError(t, closeSession.Handle(ctx, command.WorkControl{SubjectID: "owner", WorkID: ref, RequestID: uuid.NewString()}))
	}
	inputs := command.NewInputAcceptance(f.sessions, buffer)
	input := command.ExecutionInput{ID: uuid.NewString(), Text: "late input"}
	require.NoError(t, acceptance(inputs.AcceptInput(ctx, ref, input)), "input that lost to the stop before a close is accepted")
	require.Zero(t, buffer.Dropped, "the buffer is dropped once the Work enters Closing")
	work, err := f.sessions.Get(ctx, ref)
	require.NoError(t, err)
	require.Equal(t, domain.WorkStopping, work.State)
	buffer.during = nil
	require.NoError(t, acceptance(inputs.AcceptInput(ctx, ref, command.ExecutionInput{ID: uuid.NewString(), Text: "after close"})), "a Work keeping a close admits input until Closing drops it")
}

// Accepting input commits the Work against the version the acceptance read;
// a stale version commits nothing.
func TestAcceptanceCommitsTheWorkAgainstItsVersion(t *testing.T) {
	f, ref := workingSession(t)
	ctx := context.Background()
	stale, err := f.sessions.Get(ctx, ref)
	require.NoError(t, err)
	current, err := f.sessions.Get(ctx, ref)
	require.NoError(t, err)
	require.NoError(t, current.AcceptInput(false))
	require.NoError(t, f.sessions.Save(ctx, current))
	require.NoError(t, stale.AcceptInput(false))
	require.ErrorIs(t, f.sessions.Save(ctx, stale), domain.ErrConcurrentModification)
}

// acceptance is the error of accepting an input, whatever the hand-off.
func acceptance(_ command.InputHandOff, err error) error { return err }
