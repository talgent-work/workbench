package redis_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
	dispatcherredis "github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/redis"
)

func inputBuffer(t *testing.T) (*miniredis.Miniredis, *dispatcherredis.InputBuffer) {
	t.Helper()
	server := miniredis.RunT(t)
	return server, dispatcherredis.NewInputBuffer(goredis.NewClient(&goredis.Options{Addr: server.Addr()}))
}

// Intent (real): a buffer keeps entries in admission order, deduplicates by
// identity and refuses another entry under a buffered identity.
func TestInputBufferOrdersAndDeduplicatesByIdentity(t *testing.T) {
	ctx := context.Background()
	_, buffer := inputBuffer(t)
	first := command.BufferedInput{ID: uuid.NewString(), Text: "first"}
	reply := command.BufferedInput{ID: "request-1", Reply: &command.BufferedReply{Allow: true, UpdatedInputJSON: `{"answers":{}}`}}
	second := command.BufferedInput{ID: uuid.NewString(), Text: "second"}
	for _, input := range []command.BufferedInput{first, reply, second} {
		handOff, err := buffer.Append(ctx, "work", input)
		require.NoError(t, err)
		require.Equal(t, command.InputBuffered, handOff)
	}
	handOff, err := buffer.Append(ctx, "work", first)
	require.NoError(t, err)
	require.Equal(t, command.InputRepeated, handOff)
	_, err = buffer.Append(ctx, "work", command.BufferedInput{ID: first.ID, Text: "other"})
	require.ErrorIs(t, err, command.ErrInputConflict)
	_, err = buffer.Append(ctx, "work", command.BufferedInput{ID: "request-1", Reply: &command.BufferedReply{}})
	require.ErrorIs(t, err, command.ErrInputConflict, "one interaction takes one reply")

	listed, err := buffer.List(ctx, "work")
	require.NoError(t, err)
	require.Equal(t, []command.BufferedInput{first, reply, second}, listed)
	other, err := buffer.List(ctx, "other-work")
	require.NoError(t, err)
	require.Empty(t, other, "buffers are per Work")
}

// Intent (real): an entry is written and settled once; its identity is then
// remembered, so a late retry is neither buffered again nor reported twice.
func TestSettledIdentityIsRememberedAndReportedOnce(t *testing.T) {
	ctx := context.Background()
	server, buffer := inputBuffer(t)
	input := command.BufferedInput{ID: uuid.NewString(), Text: "hello"}
	_, err := buffer.Append(ctx, "work", input)
	require.NoError(t, err)
	key := dispatcherredis.InputEntry{BufferedInput: input}.Key()
	require.NoError(t, buffer.MarkWritten(ctx, "work", []string{key, "input:" + uuid.NewString()}))
	entries, err := buffer.Entries(ctx, "work")
	require.NoError(t, err)
	require.Equal(t, []dispatcherredis.InputEntry{{BufferedInput: input, Written: true}}, entries)

	removed, err := buffer.Settle(ctx, "work", key)
	require.NoError(t, err)
	require.True(t, removed)
	removed, err = buffer.Settle(ctx, "work", key)
	require.NoError(t, err)
	require.False(t, removed, "a repeated settlement is not reported again")
	handOff, err := buffer.Append(ctx, "work", input)
	require.NoError(t, err)
	require.Equal(t, command.InputSettled, handOff)
	entries, err = buffer.Entries(ctx, "work")
	require.NoError(t, err)
	require.Empty(t, entries)

	server.FastForward(25 * 60 * 60 * 1e9)
	handOff, err = buffer.Append(ctx, "work", input)
	require.NoError(t, err)
	require.Equal(t, command.InputBuffered, handOff, "the settled marker is short-lived")
}

// Intent (real): a full buffer refuses the hand-off, and Closing empties it
// and notifies the reader.
func TestFullBufferRefusesAndDropEmptiesAndNotifies(t *testing.T) {
	ctx := context.Background()
	_, buffer := inputBuffer(t)
	notifications := buffer.Appended(ctx)
	defer notifications.Close()
	_, err := notifications.Receive(ctx)
	require.NoError(t, err)
	for i := range dispatcherredis.InputBufferCapacity {
		_, err := buffer.Append(ctx, "work", command.BufferedInput{ID: uuid.NewString(), Text: fmt.Sprint(i)})
		require.NoError(t, err)
		require.Equal(t, "work", (<-notifications.Channel()).Payload)
	}
	_, err = buffer.Append(ctx, "work", command.BufferedInput{ID: uuid.NewString(), Text: "one too many"})
	require.ErrorIs(t, err, command.ErrInputBufferFull)

	require.NoError(t, buffer.Drop(ctx, "work"))
	require.Equal(t, "work", (<-notifications.Channel()).Payload)
	entries, err := buffer.Entries(ctx, "work")
	require.NoError(t, err)
	require.Empty(t, entries)
	handOff, err := buffer.Append(ctx, "work", command.BufferedInput{ID: uuid.NewString(), Text: "after closing"})
	require.NoError(t, err)
	require.Equal(t, command.InputBuffered, handOff)
}
