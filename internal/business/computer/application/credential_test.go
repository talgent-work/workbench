package application_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/computer/application"
	"github.com/talgent-work/workbench/internal/business/computer/domain"
)

// Once the credential is due, its connection rotates it: the successor
// authenticates before and after it is confirmed, the replaced credential
// only for the overlap.
func TestAConnectedMachineRotatesItsCredentialBeforeItExpires(t *testing.T) {
	f := newManaged(t)
	ctx := context.Background()
	redeemed := f.register(t, "person")
	f.now = f.now.Add(application.CredentialLifetime - domain.CredentialRenewBefore)
	authenticated, err := f.app.Authenticate(ctx, redeemed.Credential)
	require.NoError(t, err)
	require.True(t, authenticated.RotationDue)

	issued, err := f.app.RotateCredential(ctx, redeemed.ComputerID, redeemed.Credential)
	require.NoError(t, err)
	require.NotEqual(t, redeemed.Credential, issued.Credential)
	require.True(t, issued.ExpiresAt.Equal(f.now.Add(application.CredentialLifetime)))
	require.NotContains(t, f.memory.computers[redeemed.ComputerID].PendingCredential.Digest, issued.Credential, "only a digest is kept")

	// Until the machine confirms, either credential connects.
	_, err = f.app.Authenticate(ctx, redeemed.Credential)
	require.NoError(t, err)

	require.NoError(t, f.app.ConfirmCredential(ctx, redeemed.ComputerID, issued.Credential))
	require.NoError(t, f.app.ConfirmCredential(ctx, redeemed.ComputerID, issued.Credential), "confirming again does nothing")
	authenticated, err = f.app.Authenticate(ctx, issued.Credential)
	require.NoError(t, err)
	require.Equal(t, application.Authenticated{ComputerID: redeemed.ComputerID}, authenticated)
	_, err = f.app.Authenticate(ctx, redeemed.Credential)
	require.NoError(t, err, "the replaced credential overlaps")
	f.now = f.now.Add(domain.CredentialOverlap)
	_, err = f.app.Authenticate(ctx, redeemed.Credential)
	require.ErrorIs(t, err, domain.ErrCredentialRejected, "and is revoked after it")
}

// A machine that saved the successor and lost its connection before
// confirming it connects with the successor, which confirms the rotation.
func TestConnectingWithTheSuccessorConfirmsTheRotation(t *testing.T) {
	f := newManaged(t)
	ctx := context.Background()
	redeemed := f.register(t, "person")
	issued, err := f.app.RotateCredential(ctx, redeemed.ComputerID, redeemed.Credential)
	require.NoError(t, err)

	authenticated, err := f.app.Authenticate(ctx, issued.Credential)
	require.NoError(t, err)
	require.Equal(t, redeemed.ComputerID, authenticated.ComputerID)
	computer := f.memory.computers[redeemed.ComputerID]
	require.Equal(t, domain.DigestCredential(issued.Credential), computer.Credential.Digest)
	require.Empty(t, computer.PendingCredential.Digest)

	_, err = f.app.RotateCredential(ctx, redeemed.ComputerID, redeemed.Credential)
	require.ErrorIs(t, err, domain.ErrCredentialRejected, "a replaced credential rotates nothing")
}

// Removal revokes the credential for good, every Desk on the Computer leaves
// in the same commit, and Computer Removed is published once.
func TestRemovalRevokesTheCredentialAndVacatesEveryDesk(t *testing.T) {
	f := newManaged(t)
	removals := &removals{}
	f.app = application.NewApplication(application.Dependencies{Computers: f.memory, Desks: desks{f.memory}, Transactions: inline{}, Quota: quota(true), Queries: queries{f.memory},
		Removals: removals, Now: func() time.Time { return f.now }})
	ctx := context.Background()
	laptop := domain.Computer{ID: uuid.NewString(), Kind: domain.KindManaged, OwnerID: "person", Name: "Laptop", Capacity: domain.Room{Desks: 1}, Policy: domain.AdmissionQueue,
		Presence: domain.Presence{Reachable: true}, Versions: domain.ReportedVersions{TalgentComputer: "1.0.0"}, State: domain.StateActive,
		Credential: domain.Credential{Digest: domain.DigestCredential("secret"), ExpiresAt: f.now.Add(time.Hour)}}
	f.memory.computers[laptop.ID] = laptop
	run := func() application.Admission {
		admission, err := f.app.AdmitRun(ctx, application.AdmitRun{ComputerID: laptop.ID, DeskID: uuid.NewString(), WorkID: uuid.NewString(), WorkOwnerID: "person", StarterID: "person"})
		require.NoError(t, err)
		return admission
	}
	require.Equal(t, application.AnswerSeated, run().Answer)
	require.Equal(t, application.AnswerWaiting, run().Answer)

	require.ErrorIs(t, f.app.RemoveComputer(ctx, "someone-else", laptop.ID), domain.ErrComputerNotFound)
	require.NoError(t, f.app.RemoveComputer(ctx, "person", laptop.ID))
	require.NoError(t, f.app.RemoveComputer(ctx, "person", laptop.ID), "removing again changes nothing")
	require.Len(t, removals.published, 1)
	require.Equal(t, laptop.ID, removals.published[0].ComputerID)

	removed := f.memory.computers[laptop.ID]
	require.Equal(t, domain.StateRemoved, removed.State)
	require.Equal(t, domain.Room{}, removed.Occupied)
	require.Empty(t, removed.WaitingLine)
	for _, desk := range f.memory.desks {
		require.Equal(t, domain.DeskStateVacated, desk.State)
	}
	_, err := f.app.Authenticate(ctx, "secret")
	require.ErrorIs(t, err, domain.ErrComputerRemoved)
	rejected := run()
	require.Equal(t, application.AnswerRejected, rejected.Answer)
	require.Equal(t, domain.RejectedRemoved, rejected.Rejection)
	_, err = f.app.RotateCredential(ctx, laptop.ID, "secret")
	require.ErrorIs(t, err, domain.ErrComputerRemoved)
}

type removals struct{ published []application.RemovedComputer }

func (r *removals) Publish(_ context.Context, removed application.RemovedComputer) {
	r.published = append(r.published, removed)
}
