package domain

import (
	"errors"
	"fmt"
	"slices"
)

var (
	ErrServiceAuthorizationNotSelected = errors.New("agent: service authorization was not selected for this Work")
	// ErrRepositoryAuthorizationMissing refuses a start that selects
	// repositories while its authorization owner has no usable Provider
	// Authorization to reach them.
	ErrRepositoryAuthorizationMissing = errors.New("agent: the owner has no usable Provider Authorization for the selected repositories; connect one first")
)

// AuthorizationOwner is whose Provider Authorizations the Work uses: the
// person its Agent represents, or the Application that owns it; empty for a
// Work that uses none (Personal authorization scope, Application stands for
// a person).
func (s *AgentWork) AuthorizationOwner() string {
	if s.RepresentedPerson != "" {
		return s.RepresentedPerson
	}
	return s.OwningApplication
}

// ServiceAuthorization admits only a retained reference for the Work's
// authorization owner while this Work may use credentials. Marketplace
// decides current validity.
func (s *AgentWork) ServiceAuthorization(owner, service string) (WorkServiceAuthorization, error) {
	if owner == "" || owner != s.AuthorizationOwner() {
		return WorkServiceAuthorization{}, ErrInvalidWork
	}
	if err := s.AuthorizeCredentialAccess(); err != nil {
		return WorkServiceAuthorization{}, err
	}
	if s.AuthorizationSelection.OwnerUserID != owner {
		return WorkServiceAuthorization{}, ErrServiceAuthorizationNotSelected
	}
	for _, ref := range s.AuthorizationSelection.Authorizations {
		if ref.Service == service {
			return ref, nil
		}
	}
	return WorkServiceAuthorization{}, ErrServiceAuthorizationNotSelected
}

// PersonalAuthorization is an owner-scoped Marketplace fact, including records
// that currently require reconnection. Selection never freezes their credentials
// or external account and does not prove current credential applicability.
type PersonalAuthorization struct {
	SupportedTargets []string
	OwnerUserID      string `validate:"required,max=255"`
	AuthorizationID  string `validate:"required,uuid"`
	Provider         string `validate:"required,max=64"`
	Service          string `validate:"required,max=128"`
}

type WorkAuthorizationInput struct {
	OwnerUserID string
	Available   []PersonalAuthorization
}

type WorkServiceAuthorization struct {
	SupportedTargets []string
	AuthorizationID  string `validate:"required,uuid"`
	Provider         string `validate:"required,max=64"`
	Service          string `validate:"required,max=128"`
}

// WorkAuthorizationSelection is fixed at creation. Current access decisions and
// all credentials remain with Marketplace; no later Work behavior adds entries.
type WorkAuthorizationSelection struct {
	OwnerUserID    string `validate:"max=255"`
	Authorizations []WorkServiceAuthorization
}

func (s WorkAuthorizationSelection) Clone() WorkAuthorizationSelection {
	s.Authorizations = slices.Clone(s.Authorizations)
	for i := range s.Authorizations {
		s.Authorizations[i].SupportedTargets = slices.Clone(s.Authorizations[i].SupportedTargets)
	}
	return s
}

func (s WorkAuthorizationSelection) Validate() error {
	if err := validation.Struct(s); err != nil {
		return fmt.Errorf("%w: invalid authorization owner", ErrInvalidWork)
	}
	services := make(map[string]bool, len(s.Authorizations))
	providers := make(map[string]string, len(s.Authorizations))
	for _, ref := range s.Authorizations {
		if err := validation.Struct(ref); err != nil || s.OwnerUserID == "" || services[ref.Service] {
			return fmt.Errorf("%w: invalid authorization selection", ErrInvalidWork)
		}
		if id, present := providers[ref.Provider]; present && id != ref.AuthorizationID {
			return fmt.Errorf("%w: conflicting provider authorizations", ErrInvalidWork)
		}
		services[ref.Service] = true
		providers[ref.Provider] = ref.AuthorizationID
	}
	return nil
}

func (s *AgentWork) determineAuthorizationSelection(input WorkAuthorizationInput) error {
	selection := WorkAuthorizationSelection{OwnerUserID: input.OwnerUserID}
	if input.OwnerUserID != "" && input.OwnerUserID != s.AuthorizationOwner() {
		return fmt.Errorf("%w: authorization owner differs from the Work's authorization owner", ErrInvalidWork)
	}
	for _, fact := range input.Available {
		if err := validation.Struct(fact); err != nil || fact.OwnerUserID != input.OwnerUserID {
			return fmt.Errorf("%w: authorization facts are not owner scoped", ErrInvalidWork)
		}
		selection.Authorizations = append(selection.Authorizations, WorkServiceAuthorization{
			AuthorizationID: fact.AuthorizationID, Provider: fact.Provider, Service: fact.Service, SupportedTargets: slices.Clone(fact.SupportedTargets),
		})
	}
	if err := selection.Validate(); err != nil {
		return err
	}
	s.AuthorizationSelection = selection
	return nil
}
