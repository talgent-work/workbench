package domain

import "strings"

// ApplicationScopeKind is the usage scope of an Application's definition.
const ApplicationScopeKind = "application"

// ApplicationDefinitionInput is the one definition an Application's Agent
// starts with (Application definition): owned by the Application, whose
// Application Owner is no Agent Owner (Application Agent ownership), named
// after the Application (Default Application name), and starting each Work
// with the core plugin's Standalone Work role. It represents no person, so
// its Works run only on a Cloud and have no Work Shares. The Application's id
// stands among Owners beside people's Account ids: both are UUIDs Account
// assigns, so a person never matches an Application.
func ApplicationDefinitionInput(applicationID, applicationName string) AgentDefinitionInput {
	applicationID = strings.TrimSpace(applicationID)
	return AgentDefinitionInput{
		Owners:       []string{applicationID},
		AgentName:    strings.TrimSpace(applicationName),
		UsageScope:   UsageScope{Kind: ApplicationScopeKind, Reference: applicationID},
		Purpose:      StandaloneWorkPurpose,
		WorkDefaults: WorkDefaults{StartupAgent: standaloneStartupAgent},
	}
}

// AuthorizationOwner is whose Provider Authorizations this definition's
// Works use: the person its Agent represents, or the Application that owns
// it; empty for a definition whose Works use none (No inherited
// authorization).
func (a *AgentDefinition) AuthorizationOwner() string {
	if person := a.RepresentedPerson(); person != "" {
		return person
	}
	return a.OwningApplication()
}

// OwningApplication is the Application that owns this definition's Agent and
// all its Works; empty for a definition no Application owns.
func (a *AgentDefinition) OwningApplication() string {
	if a.UsageScope.Kind != ApplicationScopeKind {
		return ""
	}
	return a.UsageScope.Reference
}

// DefinitionRequester is who asks an AgentDefinition to read or change its
// configuration: a participant, and the Application whose Application Owner
// Account answered that participant is, when it is. Agent decides nothing
// about ownership itself; it takes Account's answer as a supplied fact.
type DefinitionRequester struct {
	ID string
	// OwnedApplication is the Application Account says ID owns; empty when
	// the request concerns no Application.
	OwnedApplication string
}

// admitsApplicationOwner reports whether the requester is the Application
// Owner of the Application that owns this definition's Agent. The
// Application Owner is no Agent Owner (Application Agent ownership).
func (a *AgentDefinition) admitsApplicationOwner(requester DefinitionRequester) bool {
	application := a.OwningApplication()
	return application != "" && requester.ID != "" && requester.OwnedApplication == application
}

// admitsConfiguration reports whether the requester reads and replaces the
// definition's Soul: one of its Owners, or, for an Application's Agent, its
// Application Owner (Soul authorship, Application Owner configuration).
func (a *AgentDefinition) admitsConfiguration(requester DefinitionRequester) bool {
	return a.IsOwner(requester.ID) || a.admitsApplicationOwner(requester)
}
