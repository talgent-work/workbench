package application

import (
	"slices"

	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

func AssembleWorkServiceAccess(work *domain.AgentWork, observations []query.ServiceAccessObservation, repositories []query.RepositoryPreparation) query.WorkServiceAccess {
	result := query.WorkServiceAccess{OwnerUserID: work.AuthorizationSelection.OwnerUserID, Observations: slices.Clone(observations), Repositories: slices.Clone(repositories)}
	for _, authorization := range work.AuthorizationSelection.Authorizations {
		result.SelectedServices = append(result.SelectedServices, authorization.Service)
	}
	return result
}

// AssembleWorkPluginResults lists each Plugin the Work selected, at its
// selected release, with its outcome in the current run when one was reported.
func AssembleWorkPluginResults(work *domain.AgentWork, outcomes []query.PluginResult) []query.PluginResult {
	bySlug := make(map[string]query.PluginResult, len(outcomes))
	for _, result := range outcomes {
		bySlug[result.Slug] = result
	}
	result := make([]query.PluginResult, 0, len(work.CapabilitySnapshot.Plugins))
	for _, selected := range work.CapabilitySnapshot.Plugins {
		item := bySlug[selected.Release.Slug]
		item.Slug, item.ReleaseVersion, item.Source = selected.Release.Slug, selected.Release.Version, selected.Release.Source
		result = append(result, item)
	}
	return result
}

func AssembleAgentDefinition(agent *domain.AgentDefinition) query.AgentDefinition {
	return query.AgentDefinition{Owners: slices.Clone(agent.Owners), AgentName: agent.AgentName, Purpose: agent.Purpose, UsageScope: agent.UsageScope, HomeBinding: agent.HomeBinding, Visibility: agent.Visibility, ID: agent.ID, Capabilities: agent.ProvideCapabilities()}
}

func AssembleAgentWorkEntity(session *domain.AgentWork, definition *domain.AgentDefinition) query.AgentWork {
	result := query.AgentWork{AgentName: definition.AgentName, WorkspaceReference: session.WorkspaceReference, Version: session.Version, StopRequestID: session.StopRequestReference, StopReason: session.StopReason, ID: session.ID, AgentID: session.AgentID, State: session.State,
		Capabilities: session.CapabilitySnapshot.Clone(), HarnessSessionReference: session.HarnessSessionReference, FailureReason: session.FailureReason}
	if progress := session.Progress; progress != nil {
		result.Progress = &query.WorkProgress{UpdatedAt: progress.UpdatedAt, Items: make([]query.WorkProgressItem, len(progress.Items))}
		for i, item := range progress.Items {
			result.Progress.Items[i] = query.WorkProgressItem{Content: item.Content, Status: item.Status, ActiveForm: item.ActiveForm}
		}
	}
	result.Waiting = session.Waiting()
	return result
}

func AssembleWorkIdentity(session *domain.AgentWork, definition *domain.AgentDefinition, subject string) query.WorkIdentity {
	return query.WorkIdentity{AgentName: definition.AgentName, WorkID: session.ID, AgentID: session.AgentID, SubjectID: subject, WorkspaceReference: session.WorkspaceReference,
		ProjectID: session.ProjectID, Subject: session.Subject, Purpose: definition.Purpose}
}

func AssembleWorkPluginOption(release domain.PluginRelease, required bool) query.WorkPluginOption {
	result := query.WorkPluginOption{Name: release.Slug, DisplayName: release.Slug, Required: required}
	for _, component := range release.Agents {
		result.Agents = append(result.Agents, query.WorkCapabilityOption{Name: component.Name, Description: component.Description})
	}
	for _, component := range release.Skills {
		result.Skills = append(result.Skills, query.WorkCapabilityOption{Name: component.Name, Description: component.Description, ArgumentHint: component.ArgumentHint})
	}
	return result
}
