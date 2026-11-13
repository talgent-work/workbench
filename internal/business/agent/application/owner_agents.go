package application

import (
	"context"
	"errors"
	"strings"

	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// EnsureEmployeeAgent returns a person's employee definition, establishing it
// from product defaults when missing.
func (a *Application) EnsureEmployeeAgent(ctx context.Context, employeeID, displayName string) (command.InitializedAgentDefinition, error) {
	employeeID = strings.TrimSpace(employeeID)
	if employeeID == "" {
		return command.InitializedAgentDefinition{}, domain.ErrInvalidAgentDefinition
	}
	return a.Commands.EnsureAgentDefinition.Handle(ctx, command.EnsureAgentDefinition{AgentDefinitionInput: domain.EmployeeDefinitionInput(employeeID), EmployeeDisplayName: displayName})
}

// FindEmployeeAgent locates a person's employee definition without
// establishing it.
func (a *Application) FindEmployeeAgent(ctx context.Context, employeeID string) (query.OwnedDefinition, bool, error) {
	return a.findDefinition(ctx, domain.EmployeeDefinitionInput(strings.TrimSpace(employeeID)))
}

// EnsureApplicationAgent returns an Application's one definition,
// establishing it on the Application's first need from product defaults and
// the Application's name, which only names a new definition (Application
// definition, Default Application name).
func (a *Application) EnsureApplicationAgent(ctx context.Context, applicationID, applicationName string) (command.InitializedAgentDefinition, error) {
	applicationID = strings.TrimSpace(applicationID)
	if applicationID == "" {
		return command.InitializedAgentDefinition{}, domain.ErrInvalidAgentDefinition
	}
	return a.Commands.EnsureAgentDefinition.Handle(ctx, command.EnsureAgentDefinition{AgentDefinitionInput: domain.ApplicationDefinitionInput(applicationID, applicationName)})
}

// FindApplicationAgent locates an Application's definition without
// establishing it.
func (a *Application) FindApplicationAgent(ctx context.Context, applicationID string) (query.OwnedDefinition, bool, error) {
	return a.findDefinition(ctx, domain.ApplicationDefinitionInput(strings.TrimSpace(applicationID), ""))
}

func (a *Application) findDefinition(ctx context.Context, owner domain.AgentDefinitionInput) (query.OwnedDefinition, bool, error) {
	id, err := a.DefinitionQueries.FindForPurpose(ctx, query.GetAgentDefinitionForPurpose{ScopeKind: owner.UsageScope.Kind, ScopeReference: owner.UsageScope.Reference, Purpose: owner.Purpose})
	if errors.Is(err, domain.ErrAgentDefinitionNotFound) {
		return query.OwnedDefinition{}, false, nil
	}
	if err != nil {
		return query.OwnedDefinition{}, false, err
	}
	caller, _ := owner.OwnerWorkCaller()
	return query.OwnedDefinition{AgentID: id, OwnerWorkCaller: caller}, true, nil
}

// WorkWithDefinition loads a Work and the definition it runs under, for a
// decision the Work makes itself; an unknown Work is refused as access.
func (a *Application) WorkWithDefinition(ctx context.Context, workID string) (*domain.AgentWork, *domain.AgentDefinition, error) {
	workID = strings.TrimSpace(workID)
	if workID == "" {
		return nil, nil, domain.ErrInvalidWork
	}
	work, err := a.Works.Get(ctx, workID)
	if errors.Is(err, domain.ErrWorkNotFound) {
		return nil, nil, access.ErrAccessDenied
	}
	if err != nil {
		return nil, nil, err
	}
	definition, err := a.Definitions.Get(ctx, work.AgentID)
	if err != nil {
		return nil, nil, err
	}
	return work, definition, nil
}
