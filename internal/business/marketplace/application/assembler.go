package application

import (
	"slices"
	"time"

	"github.com/talgent-work/workbench/internal/business/marketplace/application/query"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

func AssembleInstalledPluginEntity(plugin *domain.InstalledPlugin) query.InstalledPlugin {
	return query.InstalledPlugin{
		OwnerID: plugin.OwnerID, Slug: plugin.Slug, Enabled: plugin.State == domain.Enabled,
		Configuration: plugin.ManualConfiguration.Clone(), Version: plugin.Version,
	}
}

func AssembleConfigurationDTO(values map[string]any) domain.Configuration {
	result := make(domain.Configuration, len(values))
	for name, value := range values {
		result[name] = assembleValue(value)
	}
	return result
}

func assembleValue(value any) any {
	switch list := value.(type) {
	case []string:
		return slices.Clone(list)
	case []any:
		strings := make([]string, len(list))
		for index, entry := range list {
			text, ok := entry.(string)
			if !ok {
				return slices.Clone(list)
			}
			strings[index] = text
		}
		return strings
	default:
		return value
	}
}

func AssembleParameterDeclarationsDTO(parameters map[string]ParameterDeclaration) domain.ParameterDeclarations {
	result := make(domain.ParameterDeclarations, len(parameters))
	for name, parameter := range parameters {
		result[name] = domain.ParameterDeclaration{
			Type: parameter.Type, Title: parameter.Title, Description: parameter.Description,
			Required: parameter.Required, Sensitive: parameter.Sensitive, Multiple: parameter.Multiple,
			Default: assembleValue(parameter.Default), Options: slices.Clone(parameter.Options),
			Min: copyBound(parameter.Min), Max: copyBound(parameter.Max),
		}
	}
	return result
}

func copyBound(value *float64) *float64 {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

func providerAuthorizationInfo(auth *domain.ProviderAuthorization) ProviderAuthorizationInfo {
	return ProviderAuthorizationInfo{ID: auth.ID, OwnerUserID: auth.OwnerUserID, Provider: string(auth.Provider), ProviderAccountID: auth.ProviderAccountID, ProviderLogin: auth.ProviderLogin, Status: string(auth.Status)}
}

func AssemblePluginReleaseDTO(value PluginRelease) domain.PluginRelease {
	result := domain.PluginRelease{Slug: value.Slug, Source: value.Source, Version: value.Version, Description: value.Description, RetrievalRef: value.RetrievalRef, NativeEntry: slices.Clone(value.NativeEntry), Parameters: AssembleParameterDeclarationsDTO(value.Parameters)}
	for _, v := range value.Skills {
		result.Skills = append(result.Skills, domain.SkillComponent(v))
	}
	for _, v := range value.Agents {
		result.Agents = append(result.Agents, domain.AgentComponent(v))
	}
	for _, v := range value.MCPServers {
		result.MCPServers = append(result.MCPServers, domain.MCPComponent{Name: v.Name, Definition: slices.Clone(v.Definition)})
	}
	return result
}

func assembleRepositories(provider domain.Provider, repositories []domain.ScopeRepository) []ProviderRepository {
	result := make([]ProviderRepository, 0, len(repositories))
	for _, r := range repositories {
		result = append(result, ProviderRepository{Provider: provider, RepoID: r.RepoID, RepoName: r.RepoName, CloneURL: r.CloneURL, WebURL: r.WebURL, DefaultBranch: r.DefaultBranch})
	}
	return result
}

func assembleScope(provider domain.Provider, scope domain.AccessScope) ([]ProviderAccount, []ProviderPermission, time.Time) {
	accounts := make([]ProviderAccount, 0, len(scope.Accounts))
	for _, account := range scope.Accounts {
		accounts = append(accounts, ProviderAccount{Login: account.Login, Type: account.Type, AvatarURL: account.AvatarURL,
			RepositorySelection: account.RepositorySelection, SettingsURL: account.SettingsURL,
			Repositories: assembleRepositories(provider, account.Repositories)})
	}
	permissions := make([]ProviderPermission, 0, len(scope.Permissions))
	for _, permission := range scope.Permissions {
		permissions = append(permissions, ProviderPermission{Name: permission.Name, Access: permission.Access})
	}
	return accounts, permissions, scope.ObservedAt
}
