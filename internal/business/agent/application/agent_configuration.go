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

// AgentConfigurer is who reads or changes an Agent's configuration: a person,
// for their own Work Agent or as an Application Owner, or an Application, for
// its own Agent. It names one of the two, never both.
type AgentConfigurer struct {
	PersonID, ApplicationID string
}

func (c AgentConfigurer) id() string {
	if c.ApplicationID != "" {
		return c.ApplicationID
	}
	return c.PersonID
}

// DefinitionRequester is who asks the definition: the configurer, and, when a
// person asks about an Application's Agent, the Application Account says
// they own while it is active. The definition decides what they may do.
func (a *Application) DefinitionRequester(ctx context.Context, configurer AgentConfigurer, agentID string) (domain.DefinitionRequester, error) {
	_, requester, err := a.definitionFor(ctx, configurer, agentID)
	return requester, err
}

// definitionFor loads the addressed definition with who asks it.
func (a *Application) definitionFor(ctx context.Context, configurer AgentConfigurer, agentID string) (*domain.AgentDefinition, domain.DefinitionRequester, error) {
	requester := domain.DefinitionRequester{ID: strings.TrimSpace(configurer.id())}
	if requester.ID == "" {
		return nil, domain.DefinitionRequester{}, access.ErrAccessDenied
	}
	definition, err := a.Definitions.Get(ctx, strings.TrimSpace(agentID))
	if err != nil {
		return nil, domain.DefinitionRequester{}, err
	}
	application := definition.OwningApplication()
	if configurer.ApplicationID != "" || application == "" {
		return definition, requester, nil
	}
	owned, err := a.ownedApplication(ctx, requester.ID, application)
	if err != nil {
		return nil, domain.DefinitionRequester{}, err
	}
	requester.OwnedApplication = owned.ID
	return definition, requester, nil
}

// ownedApplication is the active Application person owns, by Account's
// Application Ownership Query; another person's, a removed or an unknown
// Application is no one's to configure.
func (a *Application) ownedApplication(ctx context.Context, person, applicationID string) (command.OwnedApplication, error) {
	found, err := a.Accounts.GetApplication(ctx, applicationID)
	if errors.Is(err, command.ErrApplicationUnknown) {
		return command.OwnedApplication{}, access.ErrAccessDenied
	}
	if err != nil {
		return command.OwnedApplication{}, err
	}
	if found.Removed || found.OwnerUserID != person {
		return command.OwnedApplication{}, access.ErrAccessDenied
	}
	return found, nil
}

// EnsureOwnedApplicationAgent returns the Agent of an Application the person
// owns, establishing it on its first need from the Application's name.
func (a *Application) EnsureOwnedApplicationAgent(ctx context.Context, person, applicationID string) (command.InitializedAgentDefinition, error) {
	person, applicationID = strings.TrimSpace(person), strings.TrimSpace(applicationID)
	if person == "" || applicationID == "" {
		return command.InitializedAgentDefinition{}, access.ErrAccessDenied
	}
	owned, err := a.ownedApplication(ctx, person, applicationID)
	if err != nil {
		return command.InitializedAgentDefinition{}, err
	}
	return a.EnsureApplicationAgent(ctx, owned.ID, owned.Name)
}

// AgentWorkDefaults is an Application's Agent's Work defaults and what a new
// Work of it may start with, which they choose from.
type AgentWorkDefaults struct {
	Defaults domain.WorkDefaults
	Options  query.WorkStartOptions
}

// GetAgentWorkDefaults reads an Application's Agent's Work defaults for the
// Application or its Application Owner.
func (a *Application) GetAgentWorkDefaults(ctx context.Context, configurer AgentConfigurer, agentID string) (AgentWorkDefaults, error) {
	definition, requester, err := a.definitionFor(ctx, configurer, agentID)
	if err != nil {
		return AgentWorkDefaults{}, err
	}
	defaults, err := definition.ReadWorkDefaults(requester)
	if err != nil {
		return AgentWorkDefaults{}, err
	}
	application := definition.OwningApplication()
	options, err := a.GetWorkStartOptions(ctx, query.WorkStartOptionsQuery{SubjectID: application, ApplicationID: application})
	if err != nil {
		return AgentWorkDefaults{}, err
	}
	return AgentWorkDefaults{Defaults: defaults, Options: options}, nil
}

// ChangeAgentWorkDefaults replaces an Application's Agent's Work defaults for
// the Application or its Application Owner.
func (a *Application) ChangeAgentWorkDefaults(ctx context.Context, configurer AgentConfigurer, agentID string, defaults domain.WorkDefaults) (domain.WorkDefaults, error) {
	requester, err := a.DefinitionRequester(ctx, configurer, agentID)
	if err != nil {
		return domain.WorkDefaults{}, err
	}
	return a.Commands.ChangeWorkDefaults.Handle(ctx, command.ChangeAgentWorkDefaults{AgentID: strings.TrimSpace(agentID), Requester: requester, Defaults: defaults})
}

// EnsureOwnApplicationAgent returns an Application's own Agent, establishing it on its
// first need from the Application's name; a removed or unknown Application
// has none.
func (a *Application) EnsureOwnApplicationAgent(ctx context.Context, applicationID string) (command.InitializedAgentDefinition, error) {
	found, err := a.activeApplication(ctx, applicationID)
	if err != nil {
		return command.InitializedAgentDefinition{}, err
	}
	return a.EnsureApplicationAgent(ctx, found.ID, found.Name)
}

// OwnApplicationAgent reads an Application's own Agent without establishing
// it: its definition and appearance, or, with none yet, the appearance it
// would start with and no AgentID.
func (a *Application) OwnApplicationAgent(ctx context.Context, applicationID string) (query.AgentAppearanceView, error) {
	found, err := a.activeApplication(ctx, applicationID)
	if err != nil {
		return query.AgentAppearanceView{}, err
	}
	id, exists, err := a.FindApplicationAgent(ctx, found.ID)
	if err != nil {
		return query.AgentAppearanceView{}, err
	}
	if !exists {
		return query.AgentAppearanceView{Appearance: domain.AgentAppearance{Name: strings.TrimSpace(found.Name), Icon: domain.DefaultAgentIcon}}, nil
	}
	definition, err := a.Definitions.Get(ctx, id)
	if err != nil {
		return query.AgentAppearanceView{}, err
	}
	return query.AgentAppearanceView{AgentID: definition.ID, Appearance: definition.Appearance()}, nil
}

// activeApplication is the Application as Account knows it; a removed or an
// unknown one acts no more.
func (a *Application) activeApplication(ctx context.Context, applicationID string) (command.OwnedApplication, error) {
	found, err := a.Accounts.GetApplication(ctx, strings.TrimSpace(applicationID))
	if errors.Is(err, command.ErrApplicationUnknown) || (err == nil && found.Removed) {
		return command.OwnedApplication{}, access.ErrAccessDenied
	}
	return found, err
}
