package messagehandler_test

import (
	"context"
	"errors"
	"testing"

	integrationmessage "github.com/go-jimu/components/ddd/message"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	computereventsv1 "github.com/talgent-work/workbench/gen/go/talgent/computer/events/v1"
	"github.com/talgent-work/workbench/internal/business/agent/transport/messagehandler"
)

type reach struct {
	computers []string
	removed   []string
	err       error
}

func (r *reach) ComputerRemoved(_ context.Context, computerID string) error {
	r.removed = append(r.removed, computerID)
	return r.err
}

func (r *reach) PresenceChanged(_ context.Context, computerID string) error {
	r.computers = append(r.computers, computerID)
	return r.err
}

type reconnection struct {
	computers []string
	err       error
}

func (r *reconnection) Reconnected(_ context.Context, computerID string) error {
	r.computers = append(r.computers, computerID)
	return r.err
}

func presenceFact(t *testing.T, fact proto.Message) integrationmessage.Message {
	t.Helper()
	msg, err := integrationmessage.New(integrationmessage.KindOf(fact), fact)
	require.NoError(t, err)
	return msg
}

// Either Presence change applies the Computer's current Presence to its
// Works; a failure is returned for the consumer's retry.
func TestPresenceChangesApplyTheComputersPresence(t *testing.T) {
	r, back := &reach{}, &reconnection{}
	handler := messagehandler.NewComputerPresenceHandler(r, back)
	require.Equal(t, []integrationmessage.Kind{"talgent.computer.events.v1.ComputerBecameUnreachable", "talgent.computer.events.v1.ComputerBecameReachable", "talgent.computer.events.v1.ComputerRemoved"}, handler.Listening())
	require.NoError(t, handler.Handle(context.Background(), presenceFact(t, &computereventsv1.ComputerBecameUnreachable{ComputerId: "lost"})))
	require.NoError(t, handler.Handle(context.Background(), presenceFact(t, &computereventsv1.ComputerBecameReachable{ComputerId: "back"})))
	require.Equal(t, []string{"lost", "back"}, r.computers)
	require.Equal(t, []string{"back"}, back.computers, "only a return of reach reconciles the machine and retries refused Resumes")
	require.NoError(t, handler.Handle(context.Background(), presenceFact(t, &computereventsv1.ComputerRemoved{ComputerId: "gone"})))
	require.Equal(t, []string{"gone"}, r.removed, "a removal loses the environments of the Computer's Works")

	failing := &reach{err: errors.New("computer unavailable")}
	require.ErrorIs(t, messagehandler.NewComputerPresenceHandler(failing, &reconnection{}).Handle(context.Background(), presenceFact(t, &computereventsv1.ComputerBecameReachable{ComputerId: "back"})), failing.err)
	unreconciled := &reconnection{err: errors.New("machine did not answer")}
	require.ErrorIs(t, messagehandler.NewComputerPresenceHandler(&reach{}, unreconciled).Handle(context.Background(), presenceFact(t, &computereventsv1.ComputerBecameReachable{ComputerId: "back"})), unreconciled.err, "a failed reconnection is retried")
	require.Error(t, handler.Handle(context.Background(), presenceFact(t, &computereventsv1.DeskAssigned{})))
}
