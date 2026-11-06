package messagehandler_test

import (
	"context"
	"errors"
	"testing"
	"time"

	integrationmessage "github.com/go-jimu/components/ddd/message"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	computereventsv1 "github.com/talgent-work/workbench/gen/go/talgent/computer/events/v1"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/transport/messagehandler"
)

type seating struct {
	workID, deskID string
	seatedAt       time.Time
}

type seatings struct {
	results []error
	seated  []seating
}

func (s *seatings) DeskAssigned(_ context.Context, workID, deskID string, seatedAt time.Time) error {
	s.seated = append(s.seated, seating{workID: workID, deskID: deskID, seatedAt: seatedAt})
	result := s.results[0]
	s.results = s.results[1:]
	return result
}

var seatedAt = time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC)

func deskAssigned(t *testing.T, occurredAt *timestamppb.Timestamp) integrationmessage.Message {
	t.Helper()
	fact := &computereventsv1.DeskAssigned{ComputerId: "computer", DeskId: "desk", WorkId: "work", OccurredAt: occurredAt}
	msg, err := integrationmessage.New(integrationmessage.KindOf(fact), fact)
	require.NoError(t, err)
	return msg
}

// The start begins seated when Computer seated it; a seating that lost a
// concurrent change of its Work is applied again to the Work as it is now.
func TestDeskAssignedBeginsTheWaitingStartOfItsWork(t *testing.T) {
	handler := messagehandler.NewDeskAssignedHandler(&seatings{})
	require.Equal(t, []integrationmessage.Kind{"talgent.computer.events.v1.DeskAssigned"}, handler.Listening())

	retried := &seatings{results: []error{domain.ErrConcurrentModification, nil}}
	require.NoError(t, messagehandler.NewDeskAssignedHandler(retried).Handle(context.Background(), deskAssigned(t, timestamppb.New(seatedAt))))
	want := seating{workID: "work", deskID: "desk", seatedAt: seatedAt}
	require.Equal(t, []seating{want, want}, retried.seated)

	untimed := &seatings{results: []error{nil}}
	require.NoError(t, messagehandler.NewDeskAssignedHandler(untimed).Handle(context.Background(), deskAssigned(t, nil)))
	require.True(t, untimed.seated[0].seatedAt.IsZero(), "a fact without its time leaves the seating time to Agent")

	failure := errors.New("database unavailable")
	failing := &seatings{results: []error{failure}}
	err := messagehandler.NewDeskAssignedHandler(failing).Handle(context.Background(), deskAssigned(t, nil))
	require.ErrorIs(t, err, failure, "the consumer retries any other failure")
}

// A fact for a Desk the Work does not hold yet fails, so the consumer handles
// it again and a fact that overtook the commit of its start is applied once
// it is visible.
func TestDeskAssignedForADeskNotYetHeldFailsForRetry(t *testing.T) {
	notHeld := &seatings{results: []error{command.ErrDeskNotHeld}}
	err := messagehandler.NewDeskAssignedHandler(notHeld).Handle(context.Background(), deskAssigned(t, nil))
	require.ErrorIs(t, err, command.ErrDeskNotHeld)
}
