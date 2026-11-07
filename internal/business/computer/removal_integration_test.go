//go:build integration

package computer_test

import (
	"context"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	computerprivatev1 "github.com/talgent-work/contracts/gen/computer/private/v1"
	computerpublicv1 "github.com/talgent-work/contracts/gen/computer/public/v1"
)

func registerMachine(t *testing.T, c clients) *computerpublicv1.RedeemRegistrationResponse {
	t.Helper()
	ctx := context.Background()
	started, err := c.registration.StartRegistration(ctx, &computerpublicv1.StartRegistrationRequest{MachineName: "laptop"})
	require.NoError(t, err)
	_, err = c.computers.ApproveRegistration(as(ctx, person), &computerpublicv1.ApproveRegistrationRequest{UserCode: started.GetUserCode()})
	require.NoError(t, err)
	redeemed, err := c.registration.RedeemRegistration(ctx, &computerpublicv1.RedeemRegistrationRequest{DeviceCode: started.GetDeviceCode()})
	require.NoError(t, err)
	return redeemed
}

// A rotated credential and the one it replaces are both kept and found, so
// the machine connects with whichever it holds.
func TestTheRotatedCredentialIsFoundUntilConfirmedAndAfter(t *testing.T) {
	c := start(t)
	ctx := context.Background()
	machine := registerMachine(t, c)

	rotated, err := c.connections.RotateCredential(ctx, &computerprivatev1.RotateCredentialRequest{ComputerId: machine.GetComputerId(), Credential: machine.GetCredential()})
	require.NoError(t, err)
	require.Greater(t, rotated.GetExpiresAtUnixMs(), machine.GetCredentialExpiresAtUnixMs()-1)
	_, err = c.connections.RotateCredential(ctx, &computerprivatev1.RotateCredentialRequest{ComputerId: machine.GetComputerId(), Credential: machine.GetCredential()})
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err), "the issued successor stays reserved for its connection")
	for _, credential := range []string{machine.GetCredential(), rotated.GetCredential()} {
		authenticated, err := c.connections.Authenticate(ctx, &computerprivatev1.AuthenticateRequest{Credential: credential})
		require.NoError(t, err)
		require.Equal(t, machine.GetComputerId(), authenticated.GetComputerId())
		require.False(t, authenticated.GetRotationDue())
	}
	_, err = c.connections.ConfirmCredential(ctx, &computerprivatev1.ConfirmCredentialRequest{ComputerId: machine.GetComputerId(), Credential: rotated.GetCredential()})
	require.NoError(t, err)
	_, err = c.connections.Authenticate(ctx, &computerprivatev1.AuthenticateRequest{Credential: machine.GetCredential()})
	require.NoError(t, err, "the replaced credential overlaps")
	_, err = c.connections.RotateCredential(ctx, &computerprivatev1.RotateCredentialRequest{ComputerId: machine.GetComputerId(), Credential: machine.GetCredential()})
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err), "only the current credential rotates")
}

// A control token Computer does not accept is an error, never a refusal of
// the credential the caller presented.
func TestACallerComputerRefusesIsNoCredentialRefusal(t *testing.T) {
	c := start(t)
	machine := registerMachine(t, c)
	_, err := c.strangers.Authenticate(context.Background(), &computerprivatev1.AuthenticateRequest{Credential: machine.GetCredential()})
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}

// Removing a Computer revokes its credential, vacates its Desks and rejects
// every later run; it is no longer listed, and only its owner removes it. A
// machine holding the successor it saved before confirming it is told the
// Computer was removed too.
func TestARemovedComputerRefusesItsMachineAndEveryRun(t *testing.T) {
	c := start(t)
	ctx := context.Background()
	machine := registerMachine(t, c)
	id := machine.GetComputerId()
	saved, err := c.connections.RotateCredential(ctx, &computerprivatev1.RotateCredentialRequest{ComputerId: id, Credential: machine.GetCredential()})
	require.NoError(t, err)
	_, err = c.connections.ReportReachable(ctx, &computerprivatev1.ReportReachableRequest{ComputerId: id, Versions: &computerprivatev1.ReportedVersions{TalgentComputer: "1.0.0"}})
	require.NoError(t, err)
	run := func(desk string) *computerprivatev1.AdmitRunResponse {
		admitted, err := c.admission.AdmitRun(ctx, &computerprivatev1.AdmitRunRequest{ComputerId: id, DeskId: desk, WorkId: uuid.NewString(), WorkOwnerId: person, StarterId: person})
		require.NoError(t, err)
		return admitted
	}
	desk := uuid.NewString()
	require.Equal(t, computerprivatev1.AdmissionAnswer_ADMISSION_ANSWER_SEATED, run(desk).GetAnswer())

	_, err = c.computers.RemoveComputer(as(ctx, other), &computerpublicv1.RemoveComputerRequest{ComputerId: id})
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	_, err = c.computers.RemoveComputer(as(ctx, person), &computerpublicv1.RemoveComputerRequest{ComputerId: id})
	require.NoError(t, err)
	_, err = c.computers.RemoveComputer(as(ctx, person), &computerpublicv1.RemoveComputerRequest{ComputerId: id})
	require.NoError(t, err, "removing again does nothing")

	for _, credential := range []string{machine.GetCredential(), saved.GetCredential()} {
		authenticated, err := c.connections.Authenticate(ctx, &computerprivatev1.AuthenticateRequest{Credential: credential})
		require.NoError(t, err)
		require.Equal(t, computerprivatev1.CredentialRefusal_CREDENTIAL_REFUSAL_COMPUTER_REMOVED, authenticated.GetRefusal(), "its machine is told it was removed")
	}
	_, err = c.connections.RotateCredential(ctx, &computerprivatev1.RotateCredentialRequest{ComputerId: id, Credential: machine.GetCredential()})
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	require.Equal(t, computerprivatev1.AdmissionAnswer_ADMISSION_ANSWER_REJECTED, run(uuid.NewString()).GetAnswer(), "a removed Computer rejects every run")
	standing, err := c.admission.GetDesk(ctx, &computerprivatev1.GetDeskRequest{DeskId: desk})
	require.NoError(t, err)
	require.Equal(t, computerprivatev1.DeskState_DESK_STATE_VACATED, standing.GetState(), "every Desk on it left with the removal")
	described, err := c.admission.DescribeComputer(ctx, &computerprivatev1.DescribeComputerRequest{ComputerId: id})
	require.NoError(t, err)
	require.True(t, described.GetRemoved())
	listed, err := c.computers.ListComputers(as(ctx, person), &computerpublicv1.ListComputersRequest{})
	require.NoError(t, err)
	require.Empty(t, listed.GetComputers())
}
