package application

import (
	"context"
	"errors"

	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

var ErrProviderUnavailable = errors.New("marketplace: provider unavailable")

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

// ProviderAccessObserver asks the Provider, live, what it currently grants an
// owner. Only Provider fact maintenance observes; reads use the retained scope.
type ProviderAccessObserver interface {
	ObserveAccess(ctx context.Context, ownerUserID string, provider domain.Provider) (domain.AccessScope, error)
	// RequestedPermissions reports what connecting requests; empty when unknown.
	RequestedPermissions(ctx context.Context, provider domain.Provider) ([]ProviderPermission, error)
	// ManageURL is the Provider page where the user changes reachable accounts.
	ManageURL(ctx context.Context, provider domain.Provider) (string, error)
}

// AccessScopeObservations schedules an asynchronous observation of one
// authorization's scope, merging repeated requests for the same authorization.
type AccessScopeObservations interface {
	ScheduleObservation(ctx context.Context, authorizationID string) error
}

type BeginProviderAuthorizationInput struct {
	OwnerUserID string
	Provider    domain.Provider
	RedirectURI string
}

type BeginProviderAuthorizationResult struct {
	AuthorizationURL string
}

type CompleteProviderAuthorizationInput struct {
	OwnerUserID string
	Provider    domain.Provider
	Code        string
	State       string
	RedirectURI string
}

type ProviderAuthorizer interface {
	BeginProviderAuthorization(ctx context.Context, input BeginProviderAuthorizationInput) (BeginProviderAuthorizationResult, error)
	CompleteProviderAuthorization(ctx context.Context, input CompleteProviderAuthorizationInput) (*domain.ProviderAuthorization, error)
}
