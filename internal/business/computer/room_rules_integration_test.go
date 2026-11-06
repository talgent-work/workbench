//go:build integration

package computer_test

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	computerprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/computer/private/v1"
	computerpublicv1 "github.com/talgent-work/workbench/gen/go/talgent/computer/public/v1"
)

// registerOnline registers a machine for person and connects it.
func registerOnline(t *testing.T, c clients) string {
	t.Helper()
	ctx := context.Background()
	started, err := c.registration.StartRegistration(ctx, connect.NewRequest(&computerpublicv1.StartRegistrationRequest{MachineName: "laptop"}))
	require.NoError(t, err)
	_, err = c.computers.ApproveRegistration(ctx, as(person, &computerpublicv1.ApproveRegistrationRequest{UserCode: started.Msg.GetUserCode()}))
	require.NoError(t, err)
	redeemed, err := c.registration.RedeemRegistration(ctx, connect.NewRequest(&computerpublicv1.RedeemRegistrationRequest{DeviceCode: started.Msg.GetDeviceCode()}))
	require.NoError(t, err)
	_, err = c.connections.ReportReachable(ctx, connect.NewRequest(&computerprivatev1.ReportReachableRequest{ComputerId: redeemed.Msg.GetComputerId(), Versions: &computerprivatev1.ReportedVersions{TalgentComputer: "0.1.0"}}))
	require.NoError(t, err)
	return redeemed.Msg.GetComputerId()
}

func admitRun(t *testing.T, c clients, computerID string) (string, *computerprivatev1.AdmitRunResponse) {
	t.Helper()
	desk := uuid.NewString()
	answer, err := c.admission.AdmitRun(context.Background(), connect.NewRequest(&computerprivatev1.AdmitRunRequest{ComputerId: computerID, DeskId: desk, WorkId: uuid.NewString(), WorkOwnerId: person, StarterId: person}))
	require.NoError(t, err)
	return desk, answer.Msg
}

func deskState(t *testing.T, c clients, desk string) computerprivatev1.DeskState {
	t.Helper()
	return getDesk(t, c, desk).GetState()
}

func getDesk(t *testing.T, c clients, desk string) *computerprivatev1.GetDeskResponse {
	t.Helper()
	got, err := c.admission.GetDesk(context.Background(), connect.NewRequest(&computerprivatev1.GetDeskRequest{DeskId: desk}))
	require.NoError(t, err)
	return got.Msg
}

// Its owner sets a Managed Computer's Capacity and Admission Policy; a full
// queueing Computer queues a run and seats it when a Desk leaves.
func TestAnOwnerSetsRoomRulesThatAdmissionRespects(t *testing.T) {
	c := start(t)
	ctx := context.Background()
	computerID := registerOnline(t, c)
	listed, err := c.computers.ListComputers(ctx, as(person, &computerpublicv1.ListComputersRequest{}))
	require.NoError(t, err)
	require.EqualValues(t, 1, listed.Msg.GetComputers()[0].GetCapacity(), "a new Managed Computer runs one Work at once")
	require.Equal(t, computerpublicv1.AdmissionPolicy_ADMISSION_POLICY_REJECT, listed.Msg.GetComputers()[0].GetAdmissionPolicy())

	running, seated := admitRun(t, c, computerID)
	require.Equal(t, computerprivatev1.AdmissionAnswer_ADMISSION_ANSWER_SEATED, seated.GetAnswer())
	_, rejected := admitRun(t, c, computerID)
	require.Equal(t, computerprivatev1.AdmissionAnswer_ADMISSION_ANSWER_REJECTED, rejected.GetAnswer())
	require.Equal(t, computerprivatev1.RejectionReason_REJECTION_REASON_FULL, rejected.GetReason())

	_, err = c.computers.SetRoomRules(ctx, as(other, &computerpublicv1.SetRoomRulesRequest{ComputerId: computerID, Capacity: 1, AdmissionPolicy: computerpublicv1.AdmissionPolicy_ADMISSION_POLICY_QUEUE}))
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err), "another person's Computer is not found")
	_, err = c.computers.SetRoomRules(ctx, as(person, &computerpublicv1.SetRoomRulesRequest{ComputerId: computerID, Capacity: 0, AdmissionPolicy: computerpublicv1.AdmissionPolicy_ADMISSION_POLICY_QUEUE}))
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	_, err = c.computers.SetRoomRules(ctx, as(person, &computerpublicv1.SetRoomRulesRequest{ComputerId: computerID, Capacity: 1}))
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err), "a policy is required")
	set, err := c.computers.SetRoomRules(ctx, as(person, &computerpublicv1.SetRoomRulesRequest{ComputerId: computerID, Capacity: 1, AdmissionPolicy: computerpublicv1.AdmissionPolicy_ADMISSION_POLICY_QUEUE}))
	require.NoError(t, err)
	require.Equal(t, computerpublicv1.AdmissionPolicy_ADMISSION_POLICY_QUEUE, set.Msg.GetComputer().GetAdmissionPolicy())
	require.EqualValues(t, 1, set.Msg.GetComputer().GetRunning())

	waiting, queued := admitRun(t, c, computerID)
	require.Equal(t, computerprivatev1.AdmissionAnswer_ADMISSION_ANSWER_WAITING, queued.GetAnswer())
	require.Nil(t, getDesk(t, c, waiting).GetSeatedAt(), "a waiting Desk was not seated")
	require.Equal(t, computerprivatev1.DeskState_DESK_STATE_WAITING, deskState(t, c, waiting))
	listed, err = c.computers.ListComputers(ctx, as(person, &computerpublicv1.ListComputersRequest{}))
	require.NoError(t, err)
	require.EqualValues(t, 1, listed.Msg.GetComputers()[0].GetWaiting())

	beforeSeating := time.Now().Add(-time.Second)
	_, err = c.admission.LeaveDesk(ctx, connect.NewRequest(&computerprivatev1.LeaveDeskRequest{DeskId: running}))
	require.NoError(t, err)
	seatedDesk := getDesk(t, c, waiting)
	require.Equal(t, computerprivatev1.DeskState_DESK_STATE_OCCUPIED, seatedDesk.GetState(), "the room the Desk freed seats the head of the line")
	require.True(t, seatedDesk.GetSeatedAt().AsTime().After(beforeSeating), "a seated Desk says when it was seated")
	require.Equal(t, computerprivatev1.DeskState_DESK_STATE_VACATED, deskState(t, c, running))
	_, err = c.admission.GetDesk(ctx, connect.NewRequest(&computerprivatev1.GetDeskRequest{DeskId: uuid.NewString()}))
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))

	// A withdrawn waiting run gives up its place without freeing room.
	withdrawn, _ := admitRun(t, c, computerID)
	_, err = c.admission.LeaveDesk(ctx, connect.NewRequest(&computerprivatev1.LeaveDeskRequest{DeskId: withdrawn}))
	require.NoError(t, err)
	listed, err = c.computers.ListComputers(ctx, as(person, &computerpublicv1.ListComputersRequest{}))
	require.NoError(t, err)
	require.EqualValues(t, 1, listed.Msg.GetComputers()[0].GetRunning())
	require.Zero(t, listed.Msg.GetComputers()[0].GetWaiting())
	_, err = c.computers.SetRoomRules(ctx, as(person, &computerpublicv1.SetRoomRulesRequest{ComputerId: computerID, Capacity: 1, AdmissionPolicy: computerpublicv1.AdmissionPolicy_ADMISSION_POLICY_REJECT}))
	require.NoError(t, err)
}
