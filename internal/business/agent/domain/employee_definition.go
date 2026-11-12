package domain

// Product defaults of an owner's definition, a person's employee definition
// or an Application's, which Agent establishes itself from the trusted
// identity on its first need; every caller locating that owner's definition
// gets the same one.
const (
	// StandaloneWorkPurpose is the purpose of an owner's definition, whose
	// Agent runs the Standalone Works its owner starts.
	StandaloneWorkPurpose = "work"
	// EmployeeScopeKind is the usage scope of a person's employee definition.
	EmployeeScopeKind = "employee"
	// standaloneStartupAgent is the core plugin's Standalone Work role, which
	// starts each Work of an owner's definition a caller names no other role
	// for.
	standaloneStartupAgent = "talgent:work-executor"
)

// EmployeeDefinitionInput is the definition a person's Agent starts with:
// owned by the person and representing them, the core plugin's Standalone Work
// role starting each Work a caller names no other role for, and Works private
// unless switched public within their Project. It requires no Plugin: the
// deployment's Built-in Plugins supply the core plugin.
func EmployeeDefinitionInput(employeeID string) AgentDefinitionInput {
	return AgentDefinitionInput{
		Owners:       []string{employeeID},
		UsageScope:   UsageScope{Kind: EmployeeScopeKind, Reference: employeeID},
		Purpose:      StandaloneWorkPurpose,
		Capabilities: CapabilityConfiguration{StartupAgent: standaloneStartupAgent},
	}
}
