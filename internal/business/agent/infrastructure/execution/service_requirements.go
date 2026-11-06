package execution

import (
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

type ServiceAccessStatus struct {
	Service string `json:"service"`
	Target  string `json:"target"`
	Plugin  string `json:"plugin,omitempty"`
	MCP     string `json:"mcp,omitempty"`
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
	// Temporary marks an unavailable credential whose failure confirmed
	// nothing about the authorization; it is not shown.
	Temporary bool `json:"-"`
}

type nativeServiceRequirements struct {
	Targets  []application.ServiceRequirement
	Statuses []ServiceAccessStatus
}

// Marketplace owns relationship validation; Agent consumes retained facts only.
func serviceRequirements(capabilities domain.CapabilitySnapshot, authorizations domain.WorkAuthorizationSelection) nativeServiceRequirements {
	result := nativeServiceRequirements{}
	seen := map[string]bool{}
	for _, authorization := range authorizations.Authorizations {
		for _, target := range authorization.SupportedTargets {
			result.Targets = append(result.Targets, application.ServiceRequirement{Provider: authorization.Provider, Service: authorization.Service, Target: target})
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
					result.Targets = append(result.Targets, application.ServiceRequirement{Provider: fact.Provider, Service: fact.Provider, Target: fact.Target})
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

func (r nativeServiceRequirements) statuses(result application.ServiceAccessResult) []ServiceAccessStatus {
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
