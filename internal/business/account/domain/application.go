package domain

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// ApplicationID is an Application's identity: a canonical lowercase UUIDv7,
// also its OAuth client id and the subject of its Identity Tokens.
type ApplicationID string

func ParseApplicationID(raw string) (ApplicationID, error) {
	id := strings.TrimSpace(raw)
	parsed, err := uuid.Parse(id)
	if err != nil || parsed.String() != id {
		return "", ErrApplicationNotFound
	}
	return ApplicationID(id), nil
}

// MaxApplicationName bounds an Application's Name in characters.
const MaxApplicationName = 60

func normalizeApplicationName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" || utf8.RuneCountInString(name) > MaxApplicationName {
		return "", fmt.Errorf("%w: a name has 1 to %d characters", ErrInvalidApplication, MaxApplicationName)
	}
	return name, nil
}

type ApplicationState int

const (
	// ApplicationActive obtains Identity Tokens with a valid Client Secret.
	ApplicationActive ApplicationState = iota + 1
	// ApplicationRemoved obtains nothing; terminal.
	ApplicationRemoved
)

// Application is one third-party program that calls the platform as itself.
// Its Client Secrets are owned Entities: each is valid, in its rotation
// overlap or ended, and a removed Application holds no valid one.
type Application struct {
	ID ApplicationID
	// ApplicationOwner is the person who registered it; it never
	// changes (Fixed Application Owner).
	ApplicationOwner AccountID
	Name             string
	State            ApplicationState
	RegisteredAt     time.Time
	Secrets          []*ClientSecret
	// Version is zero until the Application is first saved.
	Version int
}

// RegisterApplication begins an Application owned by the registering person
// and issues its first Client Secret, which only this call returns (Self-
// service registration, First secret at registration, Secret shown once).
func RegisterApplication(owner AccountID, name string, now time.Time) (*Application, ClientSecretValue, error) {
	if _, err := ParseAccountID(string(owner)); err != nil {
		return nil, "", fmt.Errorf("%w: the owner must be an account", ErrInvalidApplication)
	}
	name, err := normalizeApplicationName(name)
	if err != nil {
		return nil, "", err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return nil, "", err
	}
	secret, value, err := IssueClientSecret(now)
	if err != nil {
		return nil, "", err
	}
	return &Application{
		ID:               ApplicationID(id.String()),
		ApplicationOwner: owner,
		Name:             name,
		State:            ApplicationActive,
		RegisteredAt:     now.UTC(),
		Secrets:          []*ClientSecret{secret},
	}, value, nil
}

// Validate checks a reconstituted Application.
func (a *Application) Validate() error {
	if a == nil {
		return ErrInvalidApplication
	}
	if _, err := ParseApplicationID(string(a.ID)); err != nil {
		return fmt.Errorf("%w: the id must be a canonical uuid", ErrInvalidApplication)
	}
	if _, err := ParseAccountID(string(a.ApplicationOwner)); err != nil {
		return fmt.Errorf("%w: the owner must be an account", ErrInvalidApplication)
	}
	if _, err := normalizeApplicationName(a.Name); err != nil {
		return err
	}
	if a.State != ApplicationActive && a.State != ApplicationRemoved {
		return fmt.Errorf("%w: unknown state %d", ErrInvalidApplication, a.State)
	}
	current, overlapping := 0, 0
	for _, secret := range a.Secrets {
		if err := secret.Validate(); err != nil {
			return err
		}
		switch secret.State {
		case ClientSecretCurrent:
			current++
		case ClientSecretOverlapping:
			overlapping++
		}
	}
	if current > 1 || overlapping > 1 {
		return fmt.Errorf("%w: at most one current and one overlapping secret", ErrInvalidApplication)
	}
	if a.State == ApplicationRemoved && current+overlapping > 0 {
		return fmt.Errorf("%w: a removed application holds no valid secret", ErrInvalidApplication)
	}
	return nil
}

// OwnedBy tells whether person is the Application Owner, the only person who
// reads and manages it (Owner manages).
func (a *Application) OwnedBy(person AccountID) bool {
	return person != "" && a.ApplicationOwner == person
}

// ManageableBy tells whether person may read and manage the Application now:
// its Application Owner, while it is active (Owner manages, Removal is final).
func (a *Application) ManageableBy(person AccountID) bool {
	return a.State == ApplicationActive && a.OwnedBy(person)
}

// ValidSecrets are the secrets that still match at now: the current one first,
// then one still in its overlap.
func (a *Application) ValidSecrets(now time.Time) []*ClientSecret {
	valid := make([]*ClientSecret, 0, len(a.Secrets))
	for _, secret := range a.Secrets {
		if secret.valid(now) {
			valid = append(valid, secret)
		}
	}
	slices.SortStableFunc(valid, func(x, y *ClientSecret) int { return int(x.State) - int(y.State) })
	return valid
}

// RotateSecret issues a new current Client Secret, which only this call
// returns. The current secret is superseded and keeps matching for overlap;
// a secret already in an earlier overlap ends at once, so the Application
// never holds more than two (Rotation overlap, Two secrets at most, Rotation
// ends an earlier overlap, Secret shown once). Only its Application Owner
// rotates (Owner manages): any other person, like a removed Application, answers
// ErrApplicationNotFound.
func (a *Application) RotateSecret(by AccountID, now time.Time, overlap time.Duration) (ClientSecretValue, error) {
	if !a.ManageableBy(by) {
		return "", ErrApplicationNotFound
	}
	if overlap <= 0 {
		// A deployment fault, never the caller's: configuration requires a
		// positive overlap.
		return "", errors.New("account: the rotation overlap must be positive")
	}
	issued, value, err := IssueClientSecret(now)
	if err != nil {
		return "", err
	}
	for _, secret := range a.Secrets {
		switch secret.State {
		case ClientSecretOverlapping:
			secret.End()
		case ClientSecretCurrent:
			secret.Supersede(now, overlap)
		}
	}
	a.Secrets = append(a.Secrets, issued)
	return value, nil
}

// RevokeSecret ends the named Client Secret at once, while it still matches
// (Secret revocation). Tokens it already obtained stop at their expiry. Only
// its Application Owner revokes (Owner manages).
func (a *Application) RevokeSecret(by AccountID, id string, now time.Time) error {
	if !a.ManageableBy(by) {
		return ErrApplicationNotFound
	}
	for _, secret := range a.Secrets {
		if secret.ID == id && secret.valid(now) {
			secret.End()
			return nil
		}
	}
	return ErrClientSecretNotFound
}

// Rename replaces the Name its Application Owner manages it by, while it is
// active (Owner manages). It reports whether the Name changed.
func (a *Application) Rename(by AccountID, name string) (bool, error) {
	if !a.ManageableBy(by) {
		return false, ErrApplicationNotFound
	}
	name, err := normalizeApplicationName(name)
	if err != nil {
		return false, err
	}
	if name == a.Name {
		return false, nil
	}
	a.Name = name
	return true, nil
}

// IssueIdentityToken states the Application's claims for lifetime, for a
// presented secret one of its Client Secrets accepts, and records that
// secret's use (Client credentials, An Application's tokens are short). It
// issues nothing once removed (Removal is final), and the token is never
// refreshed (No Application refresh). It reports whether it recorded a use,
// which only then needs saving.
func (a *Application) IssueIdentityToken(presented ClientSecretValue, now time.Time, lifetime time.Duration) (IdentityClaims, bool, error) {
	if a.State != ApplicationActive {
		return IdentityClaims{}, false, ErrClientSecretMismatch
	}
	if lifetime <= 0 {
		return IdentityClaims{}, false, fmt.Errorf("%w: application token lifetime must be positive", ErrInvalidApplication)
	}
	digest := presented.Digest()
	for _, secret := range a.Secrets {
		if matched, recorded := secret.Match(digest, now); matched {
			now = now.UTC()
			return IdentityClaims{
				Principal: PrincipalApplication,
				Subject:   string(a.ID),
				IssuedAt:  now,
				ExpiresAt: now.Add(lifetime),
			}, recorded, nil
		}
	}
	return IdentityClaims{}, false, ErrClientSecretMismatch
}

// ClientSecretValue is the plaintext of a Client Secret: shown to its
// Application Owner once and never retained; Account keeps its digest.
type ClientSecretValue string

const (
	clientSecretPrefix = "wbs_"
	clientSecretHint   = 4
)

// ParseClientSecretValue accepts only the issued form, so arbitrary input
// never reaches a digest comparison.
func ParseClientSecretValue(raw string) (ClientSecretValue, error) {
	raw = strings.TrimSpace(raw)
	body, ok := strings.CutPrefix(raw, clientSecretPrefix)
	if !ok || !isOpaqueSecret(body) {
		return "", ErrClientSecretMismatch
	}
	return ClientSecretValue(raw), nil
}

// Digest is the one-way form a Client Secret retains.
func (v ClientSecretValue) Digest() ClientSecretDigest {
	return ClientSecretDigest(opaqueDigest(string(v)))
}

// ClientSecretDigest is a SHA-256 hex digest of a Client Secret.
type ClientSecretDigest string

type ClientSecretState int

const (
	// ClientSecretCurrent is the Application's current secret.
	ClientSecretCurrent ClientSecretState = iota + 1
	// ClientSecretOverlapping is a superseded secret in its overlap.
	ClientSecretOverlapping
	// ClientSecretEnded matches nothing; terminal.
	ClientSecretEnded
)

// ClientSecret is one credential its Application presents to obtain an
// Identity Token.
type ClientSecret struct {
	// ID is unique within its Application.
	ID           string
	SecretDigest ClientSecretDigest
	// Hint is the secret's last characters, so its owner can tell secrets
	// apart; it reveals too little to match.
	Hint     string
	State    ClientSecretState
	IssuedAt time.Time
	// OverlapEndsAt is when an overlapping secret stops matching.
	OverlapEndsAt time.Time
	// LastUsedAt is when the secret last obtained a token; zero for never.
	LastUsedAt time.Time
}

// IssueClientSecret begins a Client Secret from a fresh secret, keeping only
// its digest and returning the secret once (Issue).
func IssueClientSecret(now time.Time) (*ClientSecret, ClientSecretValue, error) {
	random, err := newOpaqueSecret()
	if err != nil {
		return nil, "", err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return nil, "", err
	}
	value := ClientSecretValue(clientSecretPrefix + random)
	return &ClientSecret{
		ID:           id.String(),
		SecretDigest: value.Digest(),
		Hint:         string(value[len(value)-clientSecretHint:]),
		State:        ClientSecretCurrent,
		IssuedAt:     now.UTC(),
	}, value, nil
}

func (s *ClientSecret) Validate() error {
	if s == nil {
		return fmt.Errorf("%w: missing client secret", ErrInvalidApplication)
	}
	if _, err := uuid.Parse(s.ID); err != nil {
		return fmt.Errorf("%w: a client secret id must be a uuid", ErrInvalidApplication)
	}
	if !validOpaqueDigest(string(s.SecretDigest)) {
		return fmt.Errorf("%w: client secret digest is malformed", ErrInvalidApplication)
	}
	switch s.State {
	case ClientSecretCurrent, ClientSecretEnded:
	case ClientSecretOverlapping:
		if s.OverlapEndsAt.IsZero() {
			return fmt.Errorf("%w: an overlapping secret needs its overlap end", ErrInvalidApplication)
		}
	default:
		return fmt.Errorf("%w: unknown client secret state %d", ErrInvalidApplication, s.State)
	}
	return nil
}

func (s *ClientSecret) valid(now time.Time) bool {
	switch s.State {
	case ClientSecretCurrent:
		return true
	case ClientSecretOverlapping:
		return now.Before(s.OverlapEndsAt)
	default:
		return false
	}
}

// Supersede replaces the current secret: it keeps matching until overlap
// after now (Rotation overlap).
func (s *ClientSecret) Supersede(now time.Time, overlap time.Duration) {
	if s.State != ClientSecretCurrent {
		return
	}
	s.State = ClientSecretOverlapping
	s.OverlapEndsAt = now.UTC().Add(overlap)
}

// End stops the secret from matching; terminal (Secret revocation, Rotation
// ends an earlier overlap, Removal is final).
func (s *ClientSecret) End() {
	if s.State == ClientSecretCurrent || s.State == ClientSecretOverlapping {
		s.State = ClientSecretEnded
	}
}

// LastUseResolution is how precisely a Client Secret's last use is kept: a
// use within it of the recorded one changes nothing, so an Application that
// fetches tokens in parallel does not write, or contend, on every grant.
const LastUseResolution = time.Minute

// Match accepts a presented secret equal to the retained digest while the
// secret is current, or overlapping before its overlap ends, and records its
// use to LastUseResolution (Match). recorded tells whether LastUsedAt moved.
func (s *ClientSecret) Match(presented ClientSecretDigest, now time.Time) (matched, recorded bool) {
	if !s.valid(now) || subtle.ConstantTimeCompare([]byte(presented), []byte(s.SecretDigest)) != 1 {
		return false, false
	}
	if !s.LastUsedAt.IsZero() && now.Sub(s.LastUsedAt) < LastUseResolution {
		return true, false
	}
	s.LastUsedAt = now.UTC()
	return true, true
}
