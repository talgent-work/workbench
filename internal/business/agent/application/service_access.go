package application

import (
	"context"
	"errors"
	"time"

	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// ServiceRequirement is a verified target contract supplied by the native
// integration adapter. It never identifies an arbitrary credential destination.
type ServiceRequirement struct{ Provider, Service, Target string }
type ServiceAccessMaterial struct {
	ServiceRequirement
	AuthorizationID string
	command.ServiceCredentialMaterial
}

// UnavailableService is a requirement no credential was supplied for;
// Temporary marks a failure that confirmed nothing about the authorization.
type UnavailableService struct {
	Service, Target, Reason string
	Temporary               bool
}
type ServiceAccessResult struct {
	Credentials []ServiceAccessMaterial
	Unavailable []UnavailableService
}

type ServiceAccess struct {
	works  domain.AgentWorkRepository
	supply command.MarketplacePort
}

func NewServiceAccess(works domain.AgentWorkRepository, supply command.MarketplacePort) *ServiceAccess {
	return &ServiceAccess{works: works, supply: supply}
}

func (a *ServiceAccess) Resolve(ctx context.Context, owner, workID string, requirements []ServiceRequirement) (ServiceAccessResult, error) {
	work, err := a.works.Get(ctx, workID)
	if err != nil {
		return ServiceAccessResult{}, err
	}
	if owner == "" || owner != work.HomeBinding.OwnerID {
		return ServiceAccessResult{}, access.ErrAccessDenied
	}
	if err := work.AuthorizeCredentialAccess(); err != nil {
		return ServiceAccessResult{}, err
	}
	result := ServiceAccessResult{}
	for _, requirement := range requirements {
		ref, err := work.ServiceAuthorization(owner, requirement.Service)
		if errors.Is(err, domain.ErrServiceAuthorizationNotSelected) {
			result.Unavailable = append(result.Unavailable, UnavailableService{Service: requirement.Service, Target: requirement.Target, Reason: "not_selected"})
			continue
		}
		if err != nil {
			return ServiceAccessResult{}, err
		}
		if ref.Provider != requirement.Provider {
			result.Unavailable = append(result.Unavailable, UnavailableService{Service: requirement.Service, Target: requirement.Target, Reason: "incompatible"})
			continue
		}
		material, err := a.supply.SupplyServiceCredential(ctx, command.ServiceCredentialRequest{OwnerUserID: owner, AuthorizationID: ref.AuthorizationID, Target: requirement.Target})
		if err != nil {
			if ctx.Err() != nil {
				return ServiceAccessResult{}, ctx.Err()
			}
			reason, temporary := refusal(err)
			result.Unavailable = append(result.Unavailable, UnavailableService{Service: requirement.Service, Target: requirement.Target, Reason: reason, Temporary: temporary})
			continue
		}
		if material.Token == "" || credentialExpired(material.ExpiresAt, time.Now()) {
			result.Unavailable = append(result.Unavailable, UnavailableService{Service: requirement.Service, Target: requirement.Target, Reason: "unavailable", Temporary: true})
			continue
		}
		result.Credentials = append(result.Credentials, ServiceAccessMaterial{ServiceRequirement: requirement, AuthorizationID: ref.AuthorizationID, ServiceCredentialMaterial: material})
	}
	current, err := a.works.Get(ctx, workID)
	if err != nil {
		return ServiceAccessResult{}, err
	}
	if err := current.AuthorizeCredentialAccess(); err != nil {
		return ServiceAccessResult{}, err
	}
	for _, material := range result.Credentials {
		ref, err := current.ServiceAuthorization(owner, material.Service)
		if err != nil || ref.AuthorizationID != material.AuthorizationID {
			return ServiceAccessResult{}, access.ErrAccessDenied
		}
	}
	return result, nil
}

// refusal classifies a supply failure; one Marketplace did not explain is
// temporary.
func refusal(err error) (string, bool) {
	var unavailable *command.ServiceCredentialUnavailable
	if errors.As(err, &unavailable) {
		return unavailable.Reason, unavailable.Temporary
	}
	return "unavailable", true
}
