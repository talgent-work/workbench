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
		Capabilities: CapabilityConfiguration{StartupAgent: standaloneStartupAgent},
	}
}

// OwningApplication is the Application that owns this definition's Agent and
// all its Works; empty for a definition no Application owns.
func (a *AgentDefinition) OwningApplication() string {
	if a.UsageScope.Kind != ApplicationScopeKind {
		return ""
	}
	return a.UsageScope.Reference
}
