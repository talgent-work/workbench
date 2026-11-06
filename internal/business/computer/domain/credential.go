package domain

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"time"
)

const (
	// CredentialRenewBefore is how long before its expiry a connected
	// Managed Computer's Computer Credential is rotated: once less than this
	// remains, the next check of its connection issues a successor.
	CredentialRenewBefore = 15 * 24 * time.Hour
	// CredentialOverlap is how long a Computer Credential its successor
	// replaced stays valid, so a connection the machine opened with it just
	// before the rotation completed is not refused; it is revoked after.
	CredentialOverlap = 15 * time.Minute
	// CredentialOffer is how long a successor a rotation issued is reserved
	// for the connection it was given to: until it is confirmed or this
	// passes, no other rotation replaces it, so a machine that saved it is
	// never left holding a successor Computer no longer accepts. A successor
	// still unconfirmed once it passed was not saved, and a later rotation
	// replaces it. The Computer Gateway waits as long for its machine's
	// answer (computergateway.SuccessorWait); both change together.
	CredentialOffer = 10 * time.Minute
)

// Credential is a Computer Credential as the platform keeps it: the digest
// of the secret issued once, and until when it is valid. IssuedAt is kept
// for the successor a rotation issued, which is reserved for CredentialOffer
// from then.
type Credential struct {
	Digest    string
	ExpiresAt time.Time
	IssuedAt  time.Time
}

// DigestCredential is the digest a Computer Credential is kept and found by.
func DigestCredential(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func (c Credential) presentedBy(digest string) bool {
	return c.Digest != "" && subtle.ConstantTimeCompare([]byte(digest), []byte(c.Digest)) == 1
}

func (c Credential) validAt(now time.Time) bool { return now.Before(c.ExpiresAt) }

func (c Credential) wellFormed(now time.Time) bool {
	return len(c.Digest) == sha256.Size*2 && c.ExpiresAt.After(now)
}

// Authenticate accepts the Computer Credential its machine presents while it
// is current: issued to this Computer, unexpired and not revoked. During a
// rotation the issued successor is accepted too, since the machine may have
// saved it before the rotation was confirmed, and a replaced credential is
// accepted for CredentialOverlap after its successor was confirmed. A
// Removed Computer refuses its credential for good.
func (c *Computer) Authenticate(secret string, now time.Time) error {
	if c.Kind != KindManaged {
		return ErrCredentialRejected
	}
	digest := DigestCredential(secret)
	var held Credential
	switch {
	case c.Credential.presentedBy(digest):
		held = c.Credential
	case c.PendingCredential.presentedBy(digest):
		held = c.PendingCredential
	case c.RetiredCredential.presentedBy(digest):
		held = c.RetiredCredential
	default:
		return ErrCredentialRejected
	}
	if c.State == StateRemoved {
		return ErrComputerRemoved
	}
	if !held.validAt(now) {
		return ErrCredentialRejected
	}
	return nil
}

// PresentsSuccessor reports whether secret is the successor a rotation
// issued and the machine has not confirmed yet.
func (c *Computer) PresentsSuccessor(secret string) bool {
	return c.PendingCredential.presentedBy(DigestCredential(secret))
}

// RotationDue reports whether the credential a connection presents is to be
// rotated over it now: it is the current Computer Credential, expiring
// within CredentialRenewBefore, and no successor is reserved for another
// connection.
func (c *Computer) RotationDue(presented string, now time.Time) bool {
	return c.rotates(DigestCredential(presented), now) && !now.Add(CredentialRenewBefore).Before(c.Credential.ExpiresAt) && !c.successorReserved(now)
}

func (c *Computer) rotates(digest string, now time.Time) bool {
	return c.Kind == KindManaged && c.State == StateActive && c.Credential.presentedBy(digest) && c.Credential.validAt(now)
}

// successorReserved reports whether a successor issued and not confirmed is
// still reserved for the connection it was given to.
func (c *Computer) successorReserved(now time.Time) bool {
	return c.PendingCredential.Digest != "" && c.PendingCredential.validAt(now) && now.Before(c.PendingCredential.IssuedAt.Add(CredentialOffer))
}

// RotateCredential issues the successor of the current Computer Credential,
// which the machine that presented the current one saves and confirms. Only
// the current credential rotates: a machine on a successor or on a replaced
// credential has nothing to rotate. While a successor issued before is
// reserved, the rotation is refused, since the machine it was given to may
// have saved it; once its reservation passed unconfirmed, it is replaced.
func (c *Computer) RotateCredential(presented string, successor Credential, now time.Time) error {
	if c.Kind != KindManaged {
		return fmt.Errorf("%w: a Cloud holds no Computer Credential", ErrInvalidComputer)
	}
	if c.State == StateRemoved {
		return ErrComputerRemoved
	}
	if !c.rotates(DigestCredential(presented), now) {
		return ErrCredentialRejected
	}
	if c.successorReserved(now) {
		return ErrRotationUnderWay
	}
	if !successor.wellFormed(now) {
		return fmt.Errorf("%w: a successor credential is unexpired", ErrInvalidComputer)
	}
	successor.IssuedAt = now.UTC()
	c.PendingCredential = successor
	return nil
}

// ConfirmCredential makes the successor its machine saved the current
// Computer Credential; the replaced one stays valid for CredentialOverlap.
// Confirming the current credential again changes nothing; the result
// reports whether the successor became current.
func (c *Computer) ConfirmCredential(secret string, now time.Time) (bool, error) {
	if c.Kind != KindManaged {
		return false, ErrCredentialRejected
	}
	if c.State == StateRemoved {
		return false, ErrComputerRemoved
	}
	digest := DigestCredential(secret)
	if c.Credential.presentedBy(digest) && c.Credential.validAt(now) {
		return false, nil
	}
	if !c.PendingCredential.presentedBy(digest) || !c.PendingCredential.validAt(now) {
		return false, ErrCredentialRejected
	}
	c.RetiredCredential = Credential{Digest: c.Credential.Digest, ExpiresAt: minTime(c.Credential.ExpiresAt, now.Add(CredentialOverlap))}
	c.Credential = Credential{Digest: c.PendingCredential.Digest, ExpiresAt: c.PendingCredential.ExpiresAt}
	c.PendingCredential = Credential{}
	return true, nil
}

// Remove removes a Managed Computer for good: every Computer Credential it
// issued is revoked, so no machine connects or runs anything as it again.
// The digests of its current, successor and replaced credentials are kept,
// so a machine presenting any of them is told it was removed. Removing it
// again changes nothing.
func (c *Computer) Remove(now time.Time) error {
	if c.Kind != KindManaged {
		return fmt.Errorf("%w: the platform owns a Cloud", ErrInvalidComputer)
	}
	if c.State == StateRemoved {
		return nil
	}
	c.State = StateRemoved
	c.record(ComputerRemoved{ComputerID: c.ID, At: now.UTC()})
	return nil
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
