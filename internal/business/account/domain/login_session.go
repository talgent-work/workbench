package domain

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// RefreshSecret is the plaintext a device presents to refresh its session. It
// is handed to the device once and never retained; the session keeps only its
// digest.
type RefreshSecret string

func NewRefreshSecret() (RefreshSecret, error) {
	secret, err := newOpaqueSecret()
	return RefreshSecret(secret), err
}

// ParseRefreshSecret accepts only the issued form so arbitrary input never
// reaches a digest lookup.
func ParseRefreshSecret(raw string) (RefreshSecret, error) {
	raw = strings.TrimSpace(raw)
	if !isOpaqueSecret(raw) {
		return "", ErrRefreshSecretUnknown
	}
	return RefreshSecret(raw), nil
}

func (s RefreshSecret) Digest() RefreshSecretDigest {
	return RefreshSecretDigest(opaqueDigest(string(s)))
}

// RefreshSecretDigest is the one-way form a Login Session retains.
type RefreshSecretDigest string

func (d RefreshSecretDigest) valid() bool { return validOpaqueDigest(string(d)) }

// SessionLifetimes are the platform's token lifetimes, supplied by the caller.
type SessionLifetimes struct {
	// Access bounds one Identity Token.
	Access time.Duration
	// Sliding is how far each refresh extends a session.
	Sliding time.Duration
	// Absolute is the hard limit from the sign-in.
	Absolute time.Duration
	// Replay is the bounded replay window: how long the last replaced
	// secret still gives back the secret that replaced it.
	Replay time.Duration
}

func (l SessionLifetimes) validate() error {
	if l.Access <= 0 || l.Sliding < l.Access || l.Absolute < l.Sliding {
		return fmt.Errorf("%w: lifetimes must satisfy 0 < access <= sliding <= absolute", ErrInvalidLoginSession)
	}
	if l.Replay <= 0 || l.Replay > l.Access {
		return fmt.Errorf("%w: lifetimes must satisfy 0 < replay <= access", ErrInvalidLoginSession)
	}
	return nil
}

// RefreshReplay is the last Refresh of a session, kept for the bounded replay
// window: the secret it replaced, the secret that replaced it, and when the
// window ends. Devices that share one refresh cookie present the replaced
// secret again when they refresh at once, or when a refresh response never
// reached them; within the window they receive the same replacing secret,
// so the cookie settles on one value. The zero value replays nothing.
type RefreshReplay struct {
	Replaced  RefreshSecretDigest
	Successor RefreshSecret
	EndsAt    time.Time
}

func (r RefreshReplay) replays(presented RefreshSecretDigest, now time.Time) bool {
	return r.Successor != "" && presented == r.Replaced && now.Before(r.EndsAt)
}

// Refreshed is what a Refresh gives the device: the refresh secret it keeps
// and the claims of its new Identity Token. Replayed says the session gave
// an earlier secret again and changed nothing.
type Refreshed struct {
	Secret   RefreshSecret
	Claims   IdentityClaims
	Replayed bool
}

type LoginSessionState int

const (
	LoginSessionActive LoginSessionState = iota + 1
	LoginSessionRevoked
)

// LoginSession is one completed sign-in on one device.
type LoginSession struct {
	ID               string
	AccountReference AccountID
	RefreshSecret    RefreshSecretDigest
	// ReplacedSecret is the secret the Refresh of this unit of work replaced;
	// it joins the session's replaced secrets when the session is saved. It is
	// not the history, which the collection keeps.
	ReplacedSecret RefreshSecretDigest
	// RefreshReplay holds the replacing secret only within the bounded
	// replay window; the collection keeps it sealed at rest.
	RefreshReplay     RefreshReplay
	ExpiresAt         time.Time
	AbsoluteExpiresAt time.Time
	RevokedAt         time.Time
	LastSeenAt        time.Time
	UserAgent         string
	Version           int
}

const maxUserAgent = 512

// OpenLoginSession starts the session of a completed sign-in.
func OpenLoginSession(account AccountID, secret RefreshSecret, userAgent string, now time.Time, lifetimes SessionLifetimes) (*LoginSession, error) {
	if err := lifetimes.validate(); err != nil {
		return nil, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	now = now.UTC()
	session := &LoginSession{
		ID:                id.String(),
		AccountReference:  account,
		RefreshSecret:     secret.Digest(),
		ExpiresAt:         now.Add(lifetimes.Sliding),
		AbsoluteExpiresAt: now.Add(lifetimes.Absolute),
		LastSeenAt:        now,
		UserAgent:         truncateUserAgent(userAgent),
	}
	if err := session.Validate(); err != nil {
		return nil, err
	}
	return session, nil
}

func truncateUserAgent(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= maxUserAgent {
		return value
	}
	return strings.ToValidUTF8(value[:maxUserAgent], "")
}

func (s *LoginSession) Validate() error {
	if s == nil {
		return ErrInvalidLoginSession
	}
	if _, err := uuid.Parse(s.ID); err != nil {
		return fmt.Errorf("%w: session id must be a uuid", ErrInvalidLoginSession)
	}
	if _, err := ParseAccountID(string(s.AccountReference)); err != nil {
		return err
	}
	if !s.RefreshSecret.valid() {
		return fmt.Errorf("%w: refresh secret digest is malformed", ErrInvalidLoginSession)
	}
	if s.ExpiresAt.After(s.AbsoluteExpiresAt) {
		return fmt.Errorf("%w: sliding expiry passes the absolute expiry", ErrInvalidLoginSession)
	}
	if s.RefreshReplay != (RefreshReplay{}) {
		replay := s.RefreshReplay
		if !replay.Replaced.valid() || !isOpaqueSecret(string(replay.Successor)) || replay.Successor.Digest() != s.RefreshSecret {
			return fmt.Errorf("%w: refresh replay must replay the current refresh secret", ErrInvalidLoginSession)
		}
	}
	return nil
}

func (s *LoginSession) State() LoginSessionState {
	if !s.RevokedAt.IsZero() {
		return LoginSessionRevoked
	}
	return LoginSessionActive
}

func (s *LoginSession) issuable(now time.Time) error {
	if s.State() == LoginSessionRevoked {
		return ErrLoginSessionRevoked
	}
	if !now.Before(s.ExpiresAt) || !now.Before(s.AbsoluteExpiresAt) {
		return ErrLoginSessionExpired
	}
	return nil
}

// IssueIdentityToken states who this session speaks for, with the supplied
// current Profile, for at most one access lifetime and never past the session.
func (s *LoginSession) IssueIdentityToken(profile Profile, now time.Time, lifetimes SessionLifetimes) (IdentityClaims, error) {
	if err := s.issuable(now); err != nil {
		return IdentityClaims{}, err
	}
	if err := profile.Validate(); err != nil {
		return IdentityClaims{}, err
	}
	now = now.UTC()
	expires := now.Add(lifetimes.Access)
	if s.ExpiresAt.Before(expires) {
		expires = s.ExpiresAt
	}
	return IdentityClaims{
		Subject:   string(s.AccountReference),
		Profile:   profile,
		Principal: PrincipalUser,
		SessionID: s.ID,
		IssuedAt:  now,
		ExpiresAt: expires,
	}, nil
}

// PresentedRefreshSecret is a secret a device presented, as the collection
// found it: the session's current secret, or one the session already replaced.
// A secret the collection finds under no session has no session to change.
type PresentedRefreshSecret struct {
	Digest   RefreshSecretDigest
	Replaced bool
}

// Refresh replaces the secret once, keeps the replay of that Refresh, and
// issues a new Identity Token. A replaced secret replays or is reuse; a
// secret this session never held is rejected without effect.
func (s *LoginSession) Refresh(presented PresentedRefreshSecret, next RefreshSecret, profile Profile, now time.Time, lifetimes SessionLifetimes) (Refreshed, error) {
	if presented.Digest != s.RefreshSecret {
		if !presented.Replaced {
			return Refreshed{}, ErrRefreshSecretUnknown
		}
		if s.RefreshReplay.replays(presented.Digest, now) {
			return s.replayRefresh(profile, now, lifetimes)
		}
		s.detectReuse(now)
		return Refreshed{}, ErrRefreshSecretReused
	}
	if err := s.issuable(now); err != nil {
		return Refreshed{}, err
	}
	if err := lifetimes.validate(); err != nil {
		return Refreshed{}, err
	}
	now = now.UTC()
	s.ReplacedSecret = s.RefreshSecret
	s.RefreshReplay = RefreshReplay{Replaced: s.RefreshSecret, Successor: next, EndsAt: now.Add(lifetimes.Replay)}
	s.RefreshSecret = next.Digest()
	s.ExpiresAt = now.Add(lifetimes.Sliding)
	if s.ExpiresAt.After(s.AbsoluteExpiresAt) {
		s.ExpiresAt = s.AbsoluteExpiresAt
	}
	s.LastSeenAt = now
	claims, err := s.IssueIdentityToken(profile, now, lifetimes)
	if err != nil {
		return Refreshed{}, err
	}
	return Refreshed{Secret: next, Claims: claims}, nil
}

// replayRefresh gives the replacing secret of the last Refresh again with a
// new Identity Token, and changes nothing: no rotation, no extended expiry.
// A revoked or expired session replays nothing.
func (s *LoginSession) replayRefresh(profile Profile, now time.Time, lifetimes SessionLifetimes) (Refreshed, error) {
	claims, err := s.IssueIdentityToken(profile, now, lifetimes)
	if err != nil {
		return Refreshed{}, err
	}
	return Refreshed{Secret: s.RefreshReplay.Successor, Claims: claims, Replayed: true}, nil
}

// detectReuse revokes the session for a replaced secret that no replay
// covers: two holders now present different secrets of one session.
func (s *LoginSession) detectReuse(now time.Time) {
	s.revoke(now)
}

// Revoke records the logout of the session's own Account. It reports whether
// this call changed the session: another Account cannot end it, and a revoked
// session stays revoked.
func (s *LoginSession) Revoke(by AccountID, now time.Time) bool {
	if by != s.AccountReference {
		return false
	}
	return s.revoke(now)
}

func (s *LoginSession) revoke(now time.Time) bool {
	if s.State() == LoginSessionRevoked {
		return false
	}
	s.RevokedAt = now.UTC()
	s.RefreshReplay = RefreshReplay{}
	return true
}
