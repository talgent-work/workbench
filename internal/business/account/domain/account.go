package domain

import (
	"fmt"
	"strings"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
)

var domainValidator = validator.New()

// AccountID is the UUIDv7 user id every other context stores.
type AccountID string

// ParseAccountID accepts only a canonical lowercase UUID, the form the gateway
// subject check admits.
func ParseAccountID(raw string) (AccountID, error) {
	id := strings.TrimSpace(raw)
	parsed, err := uuid.Parse(id)
	if err != nil || parsed.String() != id {
		return "", fmt.Errorf("%w: account id must be a canonical uuid", ErrInvalidAccount)
	}
	return AccountID(id), nil
}

// NewAccountID assigns the id of a first sign-in; it is never reused.
func NewAccountID() (AccountID, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return AccountID(id.String()), nil
}

// GitHubBinding is the GitHub user id an Account was created from.
type GitHubBinding int64

func NewGitHubBinding(githubUserID int64) (GitHubBinding, error) {
	if githubUserID <= 0 {
		return 0, fmt.Errorf("%w: github user id must be positive", ErrInvalidAccount)
	}
	return GitHubBinding(githubUserID), nil
}

// Profile is what GitHub last reported; it is replaced as a whole.
type Profile struct {
	Username    string `validate:"required,max=39"`
	Email       string `validate:"required,email,max=254"`
	DisplayName string `validate:"required,max=255"`
	AvatarURL   string `validate:"omitempty,url,max=2048"`
}

func NewProfile(username, email, displayName, avatarURL string) (Profile, error) {
	profile := Profile{
		Username:    strings.TrimSpace(username),
		Email:       strings.TrimSpace(email),
		DisplayName: strings.TrimSpace(displayName),
		AvatarURL:   strings.TrimSpace(avatarURL),
	}
	if err := profile.Validate(); err != nil {
		return Profile{}, err
	}
	return profile, nil
}

func (p Profile) Validate() error {
	if err := domainValidator.Struct(p); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidProfile, err)
	}
	return nil
}

// Account is one person's identity on the platform.
type Account struct {
	ID             AccountID
	GitHubBinding  GitHubBinding
	Profile        Profile
	CreatedAt      time.Time
	LastSignedInAt time.Time
	Version        int
}

// NewAccountFromGitHubSignIn creates the Account of a GitHub user that has
// none. Uniqueness of the binding is the collection's: a concurrent first
// sign-in that loses surfaces as ErrGitHubBindingTaken and continues as
// RecordSignIn on the winner.
func NewAccountFromGitHubSignIn(id AccountID, binding GitHubBinding, profile Profile, now time.Time) (*Account, error) {
	account := &Account{
		ID:             id,
		GitHubBinding:  binding,
		Profile:        profile,
		CreatedAt:      now.UTC(),
		LastSignedInAt: now.UTC(),
	}
	if err := account.Validate(); err != nil {
		return nil, err
	}
	return account, nil
}

func (a *Account) Validate() error {
	if a == nil {
		return ErrInvalidAccount
	}
	if _, err := ParseAccountID(string(a.ID)); err != nil {
		return err
	}
	if _, err := NewGitHubBinding(int64(a.GitHubBinding)); err != nil {
		return err
	}
	if a.CreatedAt.IsZero() || a.LastSignedInAt.Before(a.CreatedAt) {
		return fmt.Errorf("%w: sign-in times are inconsistent", ErrInvalidAccount)
	}
	return a.Profile.Validate()
}

// RecordSignIn replaces the Profile with what GitHub reported for the bound
// user and records when the sign-in completed.
func (a *Account) RecordSignIn(binding GitHubBinding, profile Profile, now time.Time) error {
	if binding != a.GitHubBinding {
		return ErrGitHubBindingMismatch
	}
	if err := profile.Validate(); err != nil {
		return err
	}
	a.Profile = profile
	if now.After(a.LastSignedInAt) {
		a.LastSignedInAt = now.UTC()
	}
	return nil
}
