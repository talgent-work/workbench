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
	var (
		base     domain.CapabilityConfiguration
		defaults domain.WorkDefaults
	)
	if input.WorkID != "" {
		work, err := a.Queries.GetWork.Handle(ctx, query.GetWork{SubjectID: input.SubjectID, WorkID: input.WorkID})
		if err != nil {
			return query.WorkStartOptions{}, err
		}
		value.DefaultStartupAgent = work.Capabilities.StartupAgent
		// The Work's own scopes decide its Built-in Plugins, not the
		// request's Work Caller.
		builtIns := a.BuiltInPlugins.Required(work.WorkCaller)
		for _, plugin := range work.Capabilities.Plugins {
			value.Plugins = append(value.Plugins, AssembleWorkPluginOption(plugin.Release, slices.Contains(builtIns, plugin.Release.Slug)))
		}
		for _, connector := range work.Capabilities.Connectors {
			value.Connectors = append(value.Connectors, domain.ConnectorOption{Name: connector.Name, Required: connector.Required, IncludedByDefault: true, UnavailableOnManaged: !connector.AvailableOnManaged()})
		}
		return value, nil
	}
	owner := domain.EmployeeDefinitionInput(strings.TrimSpace(input.EmployeeID))
	if input.ApplicationID != "" {
		owner = domain.ApplicationDefinitionInput(input.ApplicationID, "")
	}
	if err := owner.UsageScope.Validate(); err != nil {
		return query.WorkStartOptions{}, err
	}
	id, err := a.DefinitionQueries.FindForPurpose(ctx, query.GetAgentDefinitionForPurpose{ScopeKind: owner.UsageScope.Kind, ScopeReference: owner.UsageScope.Reference, Purpose: owner.Purpose})
	switch {
	case errors.Is(err, domain.ErrAgentDefinitionNotFound):
		base, defaults = owner.Capabilities.Clone(), owner.WorkDefaults
	case err != nil:
		return query.WorkStartOptions{}, err
	default:
		definition, err := a.Definitions.Get(ctx, id)
		if err != nil {
			return query.WorkStartOptions{}, err
		}
		base, defaults = definition.ProvideCapabilities(), definition.WorkDefaults
	}
	// A new Work in the name of the definition's owner is its Standalone
	// Work, whose Work Caller the definition names; only a trusted service
	// names another.
	caller := input.WorkCaller
	if caller == (domain.WorkCaller{}) {
		caller, _ = owner.OwnerWorkCaller()
	}
	base.RequiredPlugins = a.BuiltInPlugins.ComposeRequiredPlugins(caller, base.RequiredPlugins, input.RequiredPlugins)
	personal, err := a.Catalog.ListPersonalPlugins(ctx, input.SubjectID)
	if err != nil {
		return query.WorkStartOptions{}, err
	}
	slugs, err := (domain.StartupCapabilityInput{RequiredPlugins: base.RequiredPlugins, PersonalPlugins: personal}).SelectedSlugs()
	if err != nil {
		return query.WorkStartOptions{}, err
	}
	value.DefaultStartupAgent = defaults.StartupAgent
	value.DefaultModel, value.DefaultThinkingLevel = a.offeredDefaults(defaults)
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

// offeredDefaults is the definition's default model while the platform
// provides it, and its default thinking level while the model a new Work
// would take offers it (Model choice, Thinking-level default).
func (a *Application) offeredDefaults(defaults domain.WorkDefaults) (domain.WorkModelSelection, string) {
	var model domain.WorkModelSelection
	if _, provided := a.Models.Option(defaults.Model()); defaults.ModelConfigurationID != "" && provided {
		model = defaults.Model()
	}
	taken := model
	if taken == (domain.WorkModelSelection{}) {
		if first, ok := a.Models.Default(); ok {
			taken = first.Selection
		}
	}
	levels, _, _ := a.Models.ThinkingLevels(taken)
	if defaults.ThinkingLevel != "" && slices.Contains(levels, defaults.ThinkingLevel) {
		return model, defaults.ThinkingLevel
	}
	return model, ""
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
