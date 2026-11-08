package command

import "github.com/talgent-work/workbench/internal/business/agent/domain"

// ServiceAccessStatus is what a credential delivery observed of one service
// target a Cloud run may reach, or of one selected repository: whether its
// credential was available, and why not.
type ServiceAccessStatus struct {
	Service, Target, Plugin, MCP, Status, Reason string
	// Temporary marks an unavailable credential whose failure confirmed
	// nothing about the authorization; it is not shown.
	Temporary bool
}

// serviceRequirements are the service targets a run's credentials are
// supplied for, and the access status of each before supply.
type serviceRequirements struct {
	Targets  []ServiceRequirement
	Statuses []ServiceAccessStatus
}

// requiredServices derives the run's service targets from its retained
// facts only: the authorizations the Work selected and the Marketplace
// relationships of its Plugins' MCP servers, which Marketplace validated.
func requiredServices(capabilities domain.CapabilitySnapshot, authorizations domain.WorkAuthorizationSelection) serviceRequirements {
	result := serviceRequirements{}
	seen := map[string]bool{}
	for _, authorization := range authorizations.Authorizations {
		for _, target := range authorization.SupportedTargets {
			result.Targets = append(result.Targets, ServiceRequirement{Provider: authorization.Provider, Service: authorization.Service, Target: target})
			result.Statuses = append(result.Statuses, ServiceAccessStatus{Service: authorization.Service, Target: target, Status: "pending"})
			seen[authorization.Provider+"/"+target] = true
		}
	}
	for _, plugin := range capabilities.Plugins {
		for _, component := range plugin.Release.MCPServers {
			fact := component.Relationship
			status := ServiceAccessStatus{Plugin: plugin.Release.Slug, MCP: component.Name, Service: fact.Provider, Target: fact.Target, Status: "unavailable", Reason: fact.Reason}
			if fact.Status == "supported" && fact.ConnectorID != "" && fact.Authentication == "credential_proxy" {
				status.Status = "pending"
				status.Reason = ""
				key := fact.Provider + "/" + fact.Target
				if !seen[key] {
					result.Targets = append(result.Targets, ServiceRequirement{Provider: fact.Provider, Service: fact.Provider, Target: fact.Target})
					seen[key] = true
				}
			} else if status.Reason == "" {
				status.Reason = "relationship_not_registered"
			}
			result.Statuses = append(result.Statuses, status)
		}
	}
	return result
}

// statuses settles each pending status by what the supply returned.
func (r serviceRequirements) statuses(result ServiceAccessResult) []ServiceAccessStatus {
	for n := range r.Statuses {
		status := &r.Statuses[n]
		if status.Status != "pending" {
			continue
		}
		status.Status, status.Reason = "unavailable", "unavailable"
		for _, unavailable := range result.Unavailable {
			if unavailable.Target == status.Target && unavailable.Service == status.Service {
				status.Reason, status.Temporary = unavailable.Reason, unavailable.Temporary
			}
		}
		for _, material := range result.Credentials {
			if material.Target == status.Target && material.Service == status.Service {
				status.Status, status.Reason = "credential_available", ""
			}
		}
	}
	return r.Statuses
}

// unselected is the supply for a Work whose Agent represents no person, so
// it holds no personal authorization: every target is not selected.
func (r serviceRequirements) unselected() ServiceAccessResult {
	result := ServiceAccessResult{}
	for _, target := range r.Targets {
		result.Unavailable = append(result.Unavailable, UnavailableService{Service: target.Service, Target: target.Target, Reason: "not_selected"})
	}
	return result
}

// repositoryStatuses are the access statuses of a run's selected
// repositories.
func repositoryStatuses(result RepositoryAccessResult) []ServiceAccessStatus {
	statuses := make([]ServiceAccessStatus, 0, len(result.Credentials)+len(result.Unavailable))
	for _, material := range result.Credentials {
		statuses = append(statuses, ServiceAccessStatus{Service: "github", Target: "repository:" + material.RepositoryID, Status: "credential_available"})
	}
	for _, unavailable := range result.Unavailable {
		statuses = append(statuses, ServiceAccessStatus{Service: "github", Target: "repository:" + unavailable.RepositoryID, Status: "unavailable", Reason: unavailable.Reason, Temporary: unavailable.Temporary})
	}
	return statuses
}
