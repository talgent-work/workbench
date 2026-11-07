package computer

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	computerprivatev1 "github.com/talgent-work/contracts/gen/computer/private/v1"
	"github.com/talgent-work/contracts/gen/computer/private/v1/computerprivatev1connect"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

func TestCallErrorTellsAnUnknownComputerFromAnUnansweredCall(t *testing.T) {
	notFound := callError(connect.NewError(connect.CodeNotFound, "computer not found"))
	require.ErrorIs(t, notFound, domain.ErrComputerNotUsable)
	require.NotErrorIs(t, notFound, command.ErrComputerUnavailable)
	unavailable := callError(connect.NewError(connect.CodeUnavailable, "down"))
	require.ErrorIs(t, unavailable, command.ErrComputerUnavailable)
}

type desks struct {
	computerprivatev1connect.AdmissionServiceClient
	response *computerprivatev1.GetDeskResponse
}

func (d desks) GetDesk(context.Context, *computerprivatev1.GetDeskRequest) (*computerprivatev1.GetDeskResponse, error) {
	if d.response == nil {
		return nil, connect.NewError(connect.CodeNotFound, "desk not found")
	}
	return d.response, nil
}

// A seated Desk carries the time its Computer seated it; a waiting, unknown
// or untimed one gives no time.
func TestDeskSeatedCarriesTheSeatingTimeOfItsComputer(t *testing.T) {
	ctx := context.Background()
	seated := time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		response *computerprivatev1.GetDeskResponse
		at       time.Time
		isSeated bool
	}{
		{"seated", &computerprivatev1.GetDeskResponse{State: computerprivatev1.DeskState_DESK_STATE_OCCUPIED, SeatedAt: timestamppb.New(seated)}, seated, true},
		{"seated without a time", &computerprivatev1.GetDeskResponse{State: computerprivatev1.DeskState_DESK_STATE_OCCUPIED}, time.Time{}, true},
		{"waiting", &computerprivatev1.GetDeskResponse{State: computerprivatev1.DeskState_DESK_STATE_WAITING}, time.Time{}, false},
		{"unknown", nil, time.Time{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			at, isSeated, err := (&Client{admission: desks{response: tc.response}}).DeskSeated(ctx, "desk")
			require.NoError(t, err)
			require.Equal(t, tc.isSeated, isSeated)
			require.True(t, tc.at.Equal(at), "seated at %v", at)
		})
	}
}
