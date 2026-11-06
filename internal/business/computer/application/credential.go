package application

import (
	"context"
	"errors"
	"time"

	"github.com/talgent-work/workbench/internal/business/computer/domain"
)

// Authenticated is the Managed Computer a machine's credential belongs to.
type Authenticated struct {
	ComputerID string
	// RotationDue tells that the credential it presented is to be rotated
	// over its connection now.
	RotationDue bool
}

// Authenticate resolves the Computer Credential a machine presents to its
// Managed Computer. A machine presenting the successor a rotation issued has
// saved it, which confirms the rotation.
func (a *Application) Authenticate(ctx context.Context, credential string) (Authenticated, error) {
	computer, err := a.computers.FindByCredential(ctx, domain.DigestCredential(credential))
	if errors.Is(err, domain.ErrComputerNotFound) {
		return Authenticated{}, domain.ErrCredentialRejected
	}
	if err != nil {
		return Authenticated{}, err
	}
	now := a.now()
	if err := computer.Authenticate(credential, now); err != nil {
		return Authenticated{}, err
	}
	if computer.PresentsSuccessor(credential) {
		if err := a.ConfirmCredential(ctx, computer.ID, credential); err != nil {
			return Authenticated{}, err
		}
		return Authenticated{ComputerID: computer.ID}, nil
	}
	return Authenticated{ComputerID: computer.ID, RotationDue: computer.RotationDue(credential, now)}, nil
}

// IssuedCredential is a rotated Computer Credential, which exists in this
// answer only.
type IssuedCredential struct {
	Credential string
	ExpiresAt  time.Time
}

// RotateCredential issues the successor of the current Computer Credential
// its connected machine presented. The machine saves it and confirms it;
// until then either credential authenticates, so a crash on either side
// loses neither.
func (a *Application) RotateCredential(ctx context.Context, computerID, presented string) (IssuedCredential, error) {
	successor, err := secret("tc_")
	if err != nil {
		return IssuedCredential{}, err
	}
	now := a.now()
	issued := domain.Credential{Digest: domain.DigestCredential(successor), ExpiresAt: now.Add(CredentialLifetime)}
	err = a.changeComputer(ctx, computerID, func(computer *domain.Computer) error {
		return computer.RotateCredential(presented, issued, now)
	})
	if err != nil {
		return IssuedCredential{}, err
	}
	return IssuedCredential{Credential: successor, ExpiresAt: issued.ExpiresAt}, nil
}

// ConfirmCredential makes the successor its machine saved the Managed
// Computer's current Computer Credential; confirming it again does nothing.
func (a *Application) ConfirmCredential(ctx context.Context, computerID, credential string) error {
	now := a.now()
	return a.changeComputer(ctx, computerID, func(computer *domain.Computer) error {
		changed, err := computer.ConfirmCredential(credential, now)
		if err != nil {
			return err
		}
		if !changed {
			return errUnchanged
		}
		return nil
	})
}
