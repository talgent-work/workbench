package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func successorOf(now time.Time, secret string) Credential {
	return Credential{Digest: DigestCredential(secret), ExpiresAt: now.Add(30 * 24 * time.Hour)}
}

// A rotation is due once less than CredentialRenewBefore of the current
// credential's lifetime remains, and only for the current credential.
func TestRotationIsDueBeforeTheCredentialExpires(t *testing.T) {
	now := time.Now()
	computer := registered(t, now)
	computer.Credential.ExpiresAt = now.Add(CredentialRenewBefore + time.Minute)
	require.False(t, computer.RotationDue("secret", now))
	require.True(t, computer.RotationDue("secret", now.Add(time.Minute)))
	require.False(t, computer.RotationDue("other", now.Add(time.Minute)), "only the current credential rotates")
	require.NoError(t, computer.Remove(now))
	require.False(t, computer.RotationDue("secret", now.Add(time.Minute)), "a removed Computer rotates nothing")
}

// A successor issued and not confirmed is reserved for the connection it
// was given to: a second connection presenting the current credential, as
// a stale one on another replica, neither is due nor replaces it until the
// reservation passed.
func TestAnIssuedSuccessorIsReservedForItsConnection(t *testing.T) {
	now := time.Now()
	computer := registered(t, now)
	computer.Credential.ExpiresAt = now.Add(time.Hour)
	require.True(t, computer.RotationDue("secret", now))
	require.NoError(t, computer.RotateCredential("secret", successorOf(now, "first"), now))
	require.False(t, computer.RotationDue("secret", now.Add(time.Minute)))
	require.ErrorIs(t, computer.RotateCredential("secret", successorOf(now, "second"), now.Add(CredentialOffer-time.Second)), ErrRotationUnderWay)
	require.NoError(t, computer.Authenticate("first", now.Add(CredentialOffer-time.Second)), "the first machine's saved successor still connects")

	later := now.Add(CredentialOffer)
	require.True(t, computer.RotationDue("secret", later), "an unconfirmed successor was not saved")
	require.NoError(t, computer.RotateCredential("secret", successorOf(later, "second"), later))
	require.ErrorIs(t, computer.Authenticate("first", later), ErrCredentialRejected)
	require.NoError(t, computer.Authenticate("second", later))
}

// Until the machine confirms the successor, both the current credential and
// the successor authenticate: a machine that crashed before saving keeps
// the current one, one that crashed after saving presents the successor.
func TestEitherCredentialAuthenticatesUntilTheSuccessorIsConfirmed(t *testing.T) {
	now := time.Now()
	computer := registered(t, now)
	require.NoError(t, computer.RotateCredential("secret", successorOf(now, "next"), now))
	require.NoError(t, computer.Authenticate("secret", now))
	require.NoError(t, computer.Authenticate("next", now))
	require.True(t, computer.PresentsSuccessor("next"))

	changed, err := computer.ConfirmCredential("next", now)
	require.NoError(t, err)
	require.True(t, changed)
	require.False(t, computer.PresentsSuccessor("next"))
	require.Equal(t, DigestCredential("next"), computer.Credential.Digest)

	changed, err = computer.ConfirmCredential("next", now)
	require.NoError(t, err)
	require.False(t, changed, "confirming again changes nothing")

	require.NoError(t, computer.Authenticate("secret", now.Add(CredentialOverlap-time.Second)), "the replaced credential overlaps")
	require.ErrorIs(t, computer.Authenticate("secret", now.Add(CredentialOverlap)), ErrCredentialRejected, "then it is revoked")
	require.NoError(t, computer.Authenticate("next", now.Add(CredentialOverlap)))
}

// Only the current credential rotates, and a rotation once the reservation
// passed replaces a successor the machine never saved.
func TestOnlyTheCurrentCredentialRotates(t *testing.T) {
	now := time.Now()
	computer := registered(t, now)
	require.ErrorIs(t, computer.RotateCredential("other", successorOf(now, "next"), now), ErrCredentialRejected)
	require.NoError(t, computer.RotateCredential("secret", successorOf(now, "lost"), now))
	later := now.Add(CredentialOffer)
	require.NoError(t, computer.RotateCredential("secret", successorOf(later, "next"), later))
	require.ErrorIs(t, computer.Authenticate("lost", later), ErrCredentialRejected)
	require.ErrorIs(t, computer.RotateCredential("next", successorOf(later, "again"), later), ErrCredentialRejected, "a successor is confirmed, not rotated")
	_, err := computer.ConfirmCredential("lost", later)
	require.ErrorIs(t, err, ErrCredentialRejected)
	require.ErrorIs(t, computer.RotateCredential("secret", Credential{Digest: "short", ExpiresAt: now.Add(time.Hour)}, later.Add(CredentialOffer)), ErrInvalidComputer)
}

// Removal revokes every credential of the Computer for good, and is recorded
// once. A machine presenting any credential the Computer issued, the
// successor it saved and the one a confirmed successor replaced included,
// is told the Computer was removed, so it ends its Work processes.
func TestRemovalRevokesEveryCredentialForGood(t *testing.T) {
	now := time.Now()
	computer := registered(t, now)
	require.NoError(t, computer.RotateCredential("secret", successorOf(now, "next"), now))
	_, err := computer.ConfirmCredential("next", now)
	require.NoError(t, err)
	later := now.Add(CredentialOffer)
	require.NoError(t, computer.RotateCredential("next", successorOf(later, "saved"), later))
	require.NoError(t, computer.Remove(now))
	require.NoError(t, computer.Remove(now.Add(time.Minute)))
	require.Equal(t, StateRemoved, computer.State)
	for _, secret := range []string{"secret", "next", "saved"} {
		require.ErrorIs(t, computer.Authenticate(secret, later.Add(CredentialOverlap)), ErrComputerRemoved, secret)
	}
	require.ErrorIs(t, computer.Authenticate("other", now), ErrCredentialRejected)
	require.ErrorIs(t, computer.RotateCredential("next", successorOf(now, "again"), now), ErrComputerRemoved)
	_, err = computer.ConfirmCredential("saved", now)
	require.ErrorIs(t, err, ErrComputerRemoved)
	require.ErrorIs(t, computer.RegainReach(), ErrComputerRemoved)

	desk, rejection, err := computer.AdmitRun(RunAdmission{DeskID: "d", WorkID: "w", WorkOwnerID: "person", StarterID: "person", Demand: Room{Desks: 1}, Now: now})
	require.NoError(t, err)
	require.Nil(t, desk)
	require.Equal(t, RejectedRemoved, rejection, "a removed Computer rejects every run")

	events := computer.Events.Drain()
	require.Len(t, events, 1)
	require.Equal(t, ComputerRemoved{ComputerID: computer.ID, At: now.UTC()}, events[0])

	cloud, err := NewCloud("01a19bc3-075a-7c57-b42b-7fdf73b611ed", "Cloud", Room{MilliCPU: 1000, MemoryBytes: 1, Desks: 1}, AdmissionReject)
	require.NoError(t, err)
	require.ErrorIs(t, cloud.Remove(now), ErrInvalidComputer)
}
