package application

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

func (a *Application) GetWorkStartOptions(ctx context.Context, input query.WorkStartOptionsQuery) (query.WorkStartOptions, error) {
	value := query.WorkStartOptions{Models: a.Models.Provided()}
	var base domain.CapabilityConfiguration
	if input.WorkID != "" {
		work, err := a.Queries.GetWork.Handle(ctx, query.GetWork{SubjectID: input.SubjectID, WorkID: input.WorkID})
		if err != nil {
			return query.WorkStartOptions{}, err
		}
		value.DefaultStartupAgent = work.Capabilities.StartupAgent
		for _, plugin := range work.Capabilities.Plugins {
			value.Plugins = append(value.Plugins, AssembleWorkPluginOption(plugin.Release, false))
		}
		for _, connector := range work.Capabilities.Connectors {
			value.Connectors = append(value.Connectors, domain.ConnectorOption{Name: connector.Name, Required: connector.Required, IncludedByDefault: true, UnavailableOnManaged: !connector.AvailableOnManaged()})
		}
		return value, nil
	}
	employee := domain.EmployeeDefinitionInput(strings.TrimSpace(input.EmployeeID))
	if err := employee.UsageScope.Validate(); err != nil {
		return query.WorkStartOptions{}, err
	}
	id, err := a.DefinitionQueries.FindForPurpose(ctx, query.GetAgentDefinitionForPurpose{ScopeKind: employee.UsageScope.Kind, ScopeReference: employee.UsageScope.Reference, Purpose: employee.Purpose})
	switch {
	case errors.Is(err, domain.ErrAgentDefinitionNotFound):
		base = employee.Capabilities.Clone()
	case err != nil:
		return query.WorkStartOptions{}, err
	default:
		definition, err := a.Definitions.Get(ctx, id)
		if err != nil {
			return query.WorkStartOptions{}, err
		}
		base = definition.ProvideCapabilities()
	}
	base.RequiredPlugins = domain.WithRequiredPlugins(base.RequiredPlugins, input.RequiredPlugins)
	personal, err := a.Catalog.ListPersonalPlugins(ctx, input.SubjectID)
	if err != nil {
		return query.WorkStartOptions{}, err
	}
	slugs, err := (domain.StartupCapabilityInput{RequiredPlugins: base.RequiredPlugins, PersonalPlugins: personal}).SelectedSlugs()
	if err != nil {
		return query.WorkStartOptions{}, err
	}
	value.DefaultStartupAgent = base.StartupAgent
	for _, slug := range slugs {
		release, err := a.Catalog.ResolvePlugin(ctx, input.SubjectID, slug)
		if err != nil {
			return query.WorkStartOptions{}, err
		}
		value.Plugins = append(value.Plugins, AssembleWorkPluginOption(release, slices.Contains(base.RequiredPlugins, slug)))
	}
	// The same composition as creation: required entries plus the subject's
	// enabled installations of Offered entries.
	selection, err := a.Catalog.ReadConnectorSelection(ctx, input.SubjectID)
	if err != nil {
		return query.WorkStartOptions{}, err
	}
	value.Connectors, err = (domain.StartupCapabilityInput{Connectors: selection.Catalog, EnabledConnectors: selection.EnabledConnectors}).ConnectorOptions()
	if err != nil {
		return query.WorkStartOptions{}, err
	}
	return value, nil
}

const recentRepositoryLimit = 5

// ListWorkStartRepositories lists what the owner's authorization reaches now
// and puts their own recently used, still reachable repositories first. The
// trusted caller has admitted the owner to start a Work. A discovery failure
// leaves startup without repositories possible.
func (a *Application) ListWorkStartRepositories(ctx context.Context, owner string) (query.WorkStartRepositories, error) {
	if strings.TrimSpace(owner) == "" {
		return query.WorkStartRepositories{}, domain.ErrInvalidWork
	}
	var value query.WorkStartRepositories
	repositories, err := a.Catalog.ListAccessibleRepositories(ctx, owner)
	if err != nil {
		if ctx.Err() != nil {
			return query.WorkStartRepositories{}, ctx.Err()
		}
		value.RepositoriesUnavailable = true
		return value, nil
	}
	reachable := make(map[string]bool, len(repositories))
	for _, repository := range repositories {
		reachable[repository.RepoID] = true
		value.Repositories = append(value.Repositories, query.WorkRepositoryOption{ID: repository.RepoID, Provider: repository.Provider, RepoName: repository.RepoName, DefaultBranch: repository.DefaultBranch})
	}
	recent, err := a.WorkQueries.ListRecentRepositoryIDs(ctx, owner)
	if err != nil {
		return query.WorkStartRepositories{}, err
	}
	for _, id := range recent {
		if reachable[id] && len(value.RecentRepositoryIDs) < recentRepositoryLimit {
			value.RecentRepositoryIDs = append(value.RecentRepositoryIDs, id)
		}
	}
	return value, nil
}
