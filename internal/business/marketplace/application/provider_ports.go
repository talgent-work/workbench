package application

import (
	"context"
	"errors"
	"time"

	"github.com/talgent-work/workbench/internal/business/marketplace/application/credential"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

var ErrProviderUnavailable = errors.New("marketplace: provider unavailable")

// ErrCredentialRejected is a Provider refusing the credential it was shown,
// such as a token it revoked before its stated expiry.
var ErrCredentialRejected = errors.New("marketplace: provider rejected the credential")

type ProviderErrorCode string

type ProviderError struct {
	Code        ProviderErrorCode
	Retryable   bool
	Description string
	Err         error
}

func NewProviderError(code ProviderErrorCode, retryable bool, description string, err error) *ProviderError {
	if err == nil {
		err = ErrProviderUnavailable
	}
	return &ProviderError{Code: code, Retryable: retryable, Description: description, Err: err}
}

func (e *ProviderError) Error() string {
	if e == nil {
		return ""
	}
	if e.Description != "" {
		return string(e.Code) + ": " + e.Description
	}
	return string(e.Code)
}

func (e *ProviderError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func (e *ProviderError) Is(target error) bool {
	return target == ErrProviderUnavailable || errors.Is(e.Err, target)
}

type ProviderRepository struct {
	Provider      domain.Provider
	RepoID        string
	RepoName      string
	CloneURL      string
	WebURL        string
	DefaultBranch string
}

// ProviderAccount is one personal account or organization the authorization
// reached when last observed, with the repositories granted under it.
type ProviderAccount struct {
	Login, Type, AvatarURL, RepositorySelection, SettingsURL string
	Repositories                                             []ProviderRepository
}

type ProviderPermission struct{ Name, Access string }

// MarketplaceTaskQueuePort enqueues Marketplace's tasks. Observing an
// authorization's access scope runs as a task, merging repeated requests for
// the same authorization.
type MarketplaceTaskQueuePort interface {
	ScheduleAccessScopeObservation(ctx context.Context, authorizationID string) error
}

type BeginProviderAuthorizationInput struct {
	OwnerID     string
	Provider    domain.Provider
	RedirectURI string
}

type BeginProviderAuthorizationResult struct {
	AuthorizationURL string
}

type CompleteProviderAuthorizationInput struct {
	OwnerID     string
	Provider    domain.Provider
	Code        string
	State       string
	RedirectURI string
}

// GitHubPort reaches GitHub through the platform's GitHub App: it begins and
// completes an owner's authorization, observes the access scope the
// authorization reaches and materializes repository credentials for supply.
// Only Provider fact maintenance observes live; reads use the retained scope.
type GitHubPort interface {
	OAuthProviderPort
	// ObserveAccess asks GitHub, live, what it currently grants the holder of
	// the user token; ErrCredentialRejected when GitHub refuses the token.
	ObserveAccess(ctx context.Context, token string) (domain.AccessScope, error)
	// RequestedPermissions reports what connecting requests; empty when unknown.
	RequestedPermissions(ctx context.Context, provider domain.Provider) ([]ProviderPermission, error)
	// ManageURL is the Provider page where the owner changes reachable accounts.
	ManageURL(ctx context.Context, provider domain.Provider) (string, error)
	// SupplyRepositoryCredential materializes the user token as the
	// credential for one GitHub repository.
	SupplyRepositoryCredential(ctx context.Context, cloneURL, token string, expiresAt time.Time) (credential.MaterializedCredential, error)
}
