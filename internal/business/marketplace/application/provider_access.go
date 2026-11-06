package application

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/talgent-work/workbench/internal/business/marketplace/application/query"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

const (
	// accessObservationTimeout bounds one live observation of a Provider's reach.
	accessObservationTimeout = 2 * time.Minute
	// authorizationObservationTimeout bounds the observation an authorization
	// callback waits for; a slower one continues as a scheduled observation.
	authorizationObservationTimeout = 15 * time.Second
	// scopeSaveAttempts covers credential renewals saved while observing.
	scopeSaveAttempts = 3
)

// ProviderAccessView is the Provider page's read: the connection state and,
// while connected, the accounts, repositories and permissions last observed.
type ProviderAccessView struct {
	State       *query.ProviderConnectionState
	Accounts    []ProviderAccount
	Permissions []ProviderPermission
	ManageURL   string
	// ObservedAt is when the reach was last observed; zero when never.
	ObservedAt time.Time
	// AccessUnavailable reports that a requested observation failed; the last
	// observed reach is still shown and the authorization's state is unchanged.
	AccessUnavailable bool
}

func (a *PersonalConnections) BeginPersonalAuthorization(ctx context.Context, input BeginProviderAuthorizationInput) (BeginProviderAuthorizationResult, error) {
	if strings.TrimSpace(input.OwnerUserID) == "" {
		return BeginProviderAuthorizationResult{}, ErrUnauthenticated
	}
	return a.authorizer.BeginProviderAuthorization(ctx, input)
}

// ListAccessibleRepositories offers the repositories last observed as reachable
// under the owner's authorization, without asking the Provider. The Provider
// still decides each actual access when a repository is used.
func (a *PersonalConnections) ListAccessibleRepositories(ctx context.Context, actor string, provider domain.Provider) ([]ProviderRepository, error) {
	if strings.TrimSpace(actor) == "" {
		return nil, ErrUnauthenticated
	}
	auth, err := a.authorizations.GetByOwnerAndProvider(ctx, actor, provider)
	if errors.Is(err, domain.ErrProviderNotFound) {
		return nil, ErrProviderUnavailable
	}
	if err != nil {
		return nil, err
	}
	if err := auth.CredentialSupplyError(); err != nil {
		return nil, err
	}
	return assembleRepositories(auth.Provider, auth.AccessScope.Repositories()), nil
}

// GetProviderAccess describes the connection and its last observed reach.
// Permissions are those granted when observed, or those connecting requests.
func (a *PersonalConnections) GetProviderAccess(ctx context.Context, actor string, provider domain.Provider) (ProviderAccessView, error) {
	state, err := a.GetPersonalConnection(ctx, actor, provider)
	if err != nil {
		return ProviderAccessView{}, err
	}
	view := ProviderAccessView{State: state}
	if view.ManageURL, err = a.observer.ManageURL(ctx, provider); err != nil {
		view.ManageURL = ""
	}
	if state.Status != string(domain.ProviderAuthorizationActive) {
		// Unknown requested permissions are omitted rather than guessed.
		view.Permissions, _ = a.observer.RequestedPermissions(ctx, provider)
		return view, nil
	}
	auth, err := a.authorizations.GetByOwnerAndProvider(ctx, actor, provider)
	if err != nil {
		return ProviderAccessView{}, err
	}
	view.Accounts, view.Permissions, view.ObservedAt = assembleScope(auth.Provider, auth.AccessScope)
	return view, nil
}

// RefreshProviderAccess observes the owner's reach now, on the owner's request.
// A failed observation keeps the last observed reach and reports it as
// unavailable.
func (a *PersonalConnections) RefreshProviderAccess(ctx context.Context, actor string, provider domain.Provider) (ProviderAccessView, error) {
	if strings.TrimSpace(actor) == "" {
		return ProviderAccessView{}, ErrUnauthenticated
	}
	auth, err := a.authorizations.GetByOwnerAndProvider(ctx, actor, provider)
	if err != nil && !errors.Is(err, domain.ErrProviderNotFound) {
		return ProviderAccessView{}, err
	}
	var observeErr error
	if auth != nil && auth.IsActive() {
		observeErr = a.ObserveAccessScope(ctx, auth.ID)
	}
	view, err := a.GetProviderAccess(ctx, actor, provider)
	if err != nil {
		return ProviderAccessView{}, err
	}
	if observeErr != nil {
		if !errors.Is(observeErr, ErrProviderUnavailable) && !errors.Is(observeErr, domain.ErrAuthorizationReconnectRequired) {
			return ProviderAccessView{}, observeErr
		}
		view.AccessUnavailable = true
	}
	return view, nil
}

// ObserveAccessScope replaces an Active authorization's retained scope with a
// live observation. A failed observation changes nothing and is returned, so
// the caller decides whether to retry. A non-Active authorization reaches
// nothing and is left alone.
func (a *PersonalConnections) ObserveAccessScope(ctx context.Context, authorizationID string) error {
	return a.observeAccessScope(ctx, authorizationID, accessObservationTimeout)
}

func (a *PersonalConnections) observeAccessScope(ctx context.Context, authorizationID string, timeout time.Duration) error {
	auth, err := a.authorizations.Get(ctx, authorizationID)
	if errors.Is(err, domain.ErrProviderNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if !auth.IsActive() {
		return nil
	}
	observeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	scope, err := a.observer.ObserveAccess(observeCtx, auth.OwnerUserID, auth.Provider)
	if err != nil {
		return err
	}
	// Observation may renew the credential and save the authorization; apply
	// the scope to the current version.
	for attempt := 0; ; attempt++ {
		current, err := a.authorizations.Get(ctx, authorizationID)
		if errors.Is(err, domain.ErrProviderNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if !current.IsActive() {
			return nil
		}
		if err := current.ObserveAccessScope(scope); err != nil {
			return err
		}
		err = a.authorizations.Save(ctx, current)
		if !errors.Is(err, domain.ErrConcurrentModification) || attempt == scopeSaveAttempts-1 {
			return err
		}
	}
}

// observeAfterAuthorization records the reach of a newly established or
// reauthorized authorization so the owner sees it at once. Failure leaves the
// scope to the next observation.
func (a *PersonalConnections) observeAfterAuthorization(ctx context.Context, authorizationID string) {
	if err := a.observeAccessScope(context.WithoutCancel(ctx), authorizationID, authorizationObservationTimeout); err != nil && a.observations != nil {
		_ = a.observations.ScheduleObservation(context.WithoutCancel(ctx), authorizationID)
	}
}
