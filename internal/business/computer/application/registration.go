package application

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/talgent-work/workbench/internal/business/computer/domain"
)

const (
	// RegistrationTTL is how long a registration waits for approval.
	RegistrationTTL = 10 * time.Minute
	// RedeemInterval is how often Talgent Computer asks whether it was approved.
	RedeemInterval = 5 * time.Second
	// CredentialLifetime is how long a Computer Credential stays valid before
	// it must be rotated.
	CredentialLifetime = 30 * 24 * time.Hour
	// userCodeAlphabet leaves out vowels and look-alike characters, so a code
	// spells no word and reads unambiguously.
	userCodeAlphabet = "BCDFGHJKLMNPQRSTVWXZ"
	userCodeLength   = 8
)

var (
	// ErrRegistrationNotFound answers an unknown, expired or redeemed registration.
	ErrRegistrationNotFound = errors.New("computer: registration not found or expired")
	// ErrRegistrationTaken refuses to approve a registration another person approved.
	ErrRegistrationTaken = errors.New("computer: registration approved by another person")
	// ErrUserCodeTaken refuses a new registration a user code already names.
	ErrUserCodeTaken = errors.New("computer: user code taken")
)

// PendingRegistration is one registration waiting to be approved and
// redeemed. It is identified by the digest of its device code, which only
// Talgent Computer holds, and found by its user code, which the person types.
type PendingRegistration struct {
	DeviceDigest string
	UserCode     string
	ComputerID   string
	MachineName  string
	// ApprovedBy is the person who approved it; empty while pending.
	ApprovedBy string
	ExpiresAt  time.Time
}

// Registrations holds pending registrations until they expire.
type Registrations interface {
	// Open keeps a new pending registration until its expiry; a taken user
	// code fails with ErrUserCodeTaken.
	Open(ctx context.Context, pending PendingRegistration) error
	// ByUserCode and ByDevice find a pending registration, or fail with
	// ErrRegistrationNotFound.
	ByUserCode(ctx context.Context, userCode string) (PendingRegistration, error)
	ByDevice(ctx context.Context, deviceDigest string) (PendingRegistration, error)
	// Approve records its approver once; another approver fails with
	// ErrRegistrationTaken.
	Approve(ctx context.Context, userCode, personID string) error
	// Close forgets a registration.
	Close(ctx context.Context, pending PendingRegistration) error
}

// StartedRegistration is what Talgent Computer shows and redeems with.
type StartedRegistration struct {
	DeviceCode string
	UserCode   string
	ExpiresAt  time.Time
	Interval   time.Duration
}

// StartRegistration opens a registration for a machine, proposing its name.
func (a *Application) StartRegistration(ctx context.Context, machineName string) (StartedRegistration, error) {
	name := strings.TrimSpace(machineName)
	if runes := []rune(name); len(runes) > domain.MaxNameLength {
		name = string(runes[:domain.MaxNameLength])
	}
	if name == "" {
		name = "Computer"
	}
	computerID, err := uuid.NewV7()
	if err != nil {
		return StartedRegistration{}, err
	}
	expiresAt := a.now().Add(RegistrationTTL)
	for range attempts {
		deviceCode, err := secret("tcd_")
		if err != nil {
			return StartedRegistration{}, err
		}
		userCode, err := newUserCode()
		if err != nil {
			return StartedRegistration{}, err
		}
		err = a.registrations.Open(ctx, PendingRegistration{DeviceDigest: domain.DigestCredential(deviceCode), UserCode: userCode, ComputerID: computerID.String(), MachineName: name, ExpiresAt: expiresAt})
		if errors.Is(err, ErrUserCodeTaken) {
			continue
		}
		if err != nil {
			return StartedRegistration{}, err
		}
		return StartedRegistration{DeviceCode: deviceCode, UserCode: userCode, ExpiresAt: expiresAt, Interval: RedeemInterval}, nil
	}
	return StartedRegistration{}, errors.New("computer: no free user code")
}

// NormalizeUserCode reads a user code as typed: case, spaces and the dash
// between its halves do not matter.
func NormalizeUserCode(code string) string {
	return strings.Map(func(r rune) rune {
		if r == '-' || r == ' ' {
			return -1
		}
		return r
	}, strings.ToUpper(strings.TrimSpace(code)))
}

// RegistrationToApprove is a pending registration as the person about to
// approve it sees it.
type RegistrationToApprove struct {
	MachineName string
	ExpiresAt   time.Time
	// Approved tells that this person approved it already.
	Approved bool
}

// GetRegistration reads a pending registration for the person about to
// approve it; one another person approved is ErrRegistrationTaken.
func (a *Application) GetRegistration(ctx context.Context, userCode, personID string) (RegistrationToApprove, error) {
	pending, err := a.registrations.ByUserCode(ctx, NormalizeUserCode(userCode))
	if err != nil {
		return RegistrationToApprove{}, err
	}
	if pending.ApprovedBy != "" && pending.ApprovedBy != personID {
		return RegistrationToApprove{}, ErrRegistrationTaken
	}
	return RegistrationToApprove{MachineName: pending.MachineName, ExpiresAt: pending.ExpiresAt, Approved: pending.ApprovedBy == personID}, nil
}

// ApproveRegistration approves a pending registration as the signed-in
// person, who will own the Computer it registers.
func (a *Application) ApproveRegistration(ctx context.Context, userCode, personID string) error {
	if strings.TrimSpace(personID) == "" {
		return ErrRegistrationNotFound
	}
	return a.registrations.Approve(ctx, NormalizeUserCode(userCode), personID)
}

// Redemption is a redeemed registration: the new Computer and its Computer
// Credential, which exists in this answer only.
type Redemption struct {
	Pending    bool
	ComputerID string
	Name       string
	Credential string
	ExpiresAt  time.Time
}

// RedeemRegistration answers pending until the registration is approved,
// then registers its Managed Computer and issues its credential. The
// Computer's identity was fixed when the registration opened, so a second
// redemption finds it registered and gets nothing.
func (a *Application) RedeemRegistration(ctx context.Context, deviceCode string) (Redemption, error) {
	pending, err := a.registrations.ByDevice(ctx, domain.DigestCredential(strings.TrimSpace(deviceCode)))
	if err != nil {
		return Redemption{}, err
	}
	if pending.ApprovedBy == "" {
		return Redemption{Pending: true}, nil
	}
	credential, err := secret("tc_")
	if err != nil {
		return Redemption{}, err
	}
	now := a.now()
	computer, err := domain.Register(domain.Registration{
		ComputerID: pending.ComputerID, OwnerID: pending.ApprovedBy, Name: pending.MachineName, Now: now,
		Credential: domain.Credential{Digest: domain.DigestCredential(credential), ExpiresAt: now.Add(CredentialLifetime)},
	})
	if err != nil {
		return Redemption{}, err
	}
	if err := a.computers.Save(ctx, computer); err != nil {
		if errors.Is(err, domain.ErrConcurrentModification) {
			return Redemption{}, ErrRegistrationNotFound
		}
		return Redemption{}, err
	}
	// The Computer is registered; a registration left behind only expires,
	// and redeeming it again finds the Computer taken.
	if err := a.registrations.Close(ctx, pending); err != nil {
		a.logger.WarnContext(ctx, "computer: registration left to expire", "computer_id", computer.ID, "error", err)
	}
	return Redemption{ComputerID: computer.ID, Name: computer.Name, Credential: credential, ExpiresAt: computer.Credential.ExpiresAt}, nil
}

func secret(prefix string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

func newUserCode() (string, error) {
	raw := make([]byte, userCodeLength)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	code := make([]byte, userCodeLength)
	for i, b := range raw {
		// 256 is not a multiple of the alphabet; the bias is immaterial for
		// a code that lives ten minutes behind a sign-in.
		code[i] = userCodeAlphabet[int(b)%len(userCodeAlphabet)]
	}
	return string(code), nil
}
