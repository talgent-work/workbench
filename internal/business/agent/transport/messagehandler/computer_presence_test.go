package messagehandler_test

import (
	"context"
	"errors"
	"testing"

	integrationmessage "github.com/go-jimu/components/ddd/message"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	computerintegrationv1 "github.com/talgent-work/contracts/gen/computer/integration/v1"
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
	require.Equal(t, []integrationmessage.Kind{"computer.integration.v1.ComputerBecameUnreachableV1", "computer.integration.v1.ComputerBecameReachableV1", "computer.integration.v1.ComputerRemovedV1"}, handler.Listening())
	require.NoError(t, handler.Handle(context.Background(), presenceFact(t, &computerintegrationv1.ComputerBecameUnreachableV1{ComputerId: "lost"})))
	require.NoError(t, handler.Handle(context.Background(), presenceFact(t, &computerintegrationv1.ComputerBecameReachableV1{ComputerId: "back"})))
	require.Equal(t, []string{"lost", "back"}, r.computers)
	require.Equal(t, []string{"back"}, back.computers, "only a return of reach reconciles the machine and retries refused Resumes")
	require.NoError(t, handler.Handle(context.Background(), presenceFact(t, &computerintegrationv1.ComputerRemovedV1{ComputerId: "gone"})))
	require.Equal(t, []string{"gone"}, r.removed, "a removal loses the environments of the Computer's Works")

	failing := &reach{err: errors.New("computer unavailable")}
	require.ErrorIs(t, messagehandler.NewComputerPresenceHandler(failing, &reconnection{}).Handle(context.Background(), presenceFact(t, &computerintegrationv1.ComputerBecameReachableV1{ComputerId: "back"})), failing.err)
	unreconciled := &reconnection{err: errors.New("machine did not answer")}
	require.ErrorIs(t, messagehandler.NewComputerPresenceHandler(&reach{}, unreconciled).Handle(context.Background(), presenceFact(t, &computerintegrationv1.ComputerBecameReachableV1{ComputerId: "back"})), unreconciled.err, "a failed reconnection is retried")
	require.Error(t, handler.Handle(context.Background(), presenceFact(t, &computerintegrationv1.DeskAssignedV1{})))
}
