package application

import (
	"context"
	"errors"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

type CreateAgentDefinition struct {
	Owners       []string
	AgentName    string
	Purpose      string
	UsageScope   domain.UsageScope
	Visibility   domain.VisibilityRule
	Capabilities domain.CapabilityConfiguration
	WorkDefaults domain.WorkDefaults
}

func (a *Application) CreateAgentDefinition(ctx context.Context, request CreateAgentDefinition) (string, error) {
	agent, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: request.Owners, AgentName: request.AgentName, Purpose: request.Purpose, UsageScope: request.UsageScope, Visibility: request.Visibility, Capabilities: request.Capabilities, WorkDefaults: request.WorkDefaults})
	if err != nil {
		return "", err
	}
	if _, err := agent.EstablishAppearance("", request.AgentName); err != nil {
		return "", err
	}
	if err := a.Definitions.Save(ctx, agent); err != nil {
		return "", err
	}
	return agent.ID, nil
}

// ChangeAgentDefinitionCapabilities replaces a definition's base Prompt,
// required Plugins and the startup Agent of its Work defaults.
type ChangeAgentDefinitionCapabilities struct {
	AgentID      string
	Capabilities domain.CapabilityConfiguration
	StartupAgent string
}

func (a *Application) ChangeAgentDefinitionCapabilities(ctx context.Context, request ChangeAgentDefinitionCapabilities) error {
	agent, err := a.Definitions.Get(ctx, request.AgentID)
	if err != nil {
		return err
	}
	if err := agent.ChangeCapabilities(request.Capabilities, request.StartupAgent); err != nil {
		return err
	}
	return a.Definitions.Save(ctx, agent)
}

// TransferAgentDefinitionOwners replaces a definition's Owners, so every one
// of its Works answers to them, retrying on a concurrent change.
type TransferAgentDefinitionOwners struct {
	AgentID string
	Owners  []string
}

func (a *Application) TransferAgentDefinitionOwners(ctx context.Context, request TransferAgentDefinitionOwners) (bool, error) {
	for range 8 {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		agent, err := a.Definitions.Get(ctx, request.AgentID)
		if err != nil {
			return false, err
		}
		changed, err := agent.TransferOwners(request.Owners)
		if err != nil || !changed {
			return false, err
		}
		if err = a.Definitions.Save(ctx, agent); errors.Is(err, domain.ErrConcurrentModification) {
			continue
		}
		return err == nil, err
	}
	return false, domain.ErrConcurrentModification
}
