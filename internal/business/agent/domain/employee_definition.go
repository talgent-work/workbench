package domain

// Product defaults of a person's employee definition, which Agent
// establishes itself from the trusted identity on its first need; every
// caller locating that person's definition gets the same one.
const (
	// EmployeeWorkPurpose is the purpose of a person's employee definition.
	EmployeeWorkPurpose = "work"
	// EmployeeScopeKind is the usage scope of a person's employee definition.
	EmployeeScopeKind    = "employee"
	employeeStartupAgent = "talgent-runtime:personal-work-executor"
)

// EmployeeDefinitionInput is the definition a person's Agent starts with:
// owned by the person and representing them, the core plugin's Personal Work
// role starting each Work a caller names no other role for, and Works private
// unless switched public within their Project. It requires no Plugin: the
// deployment's Built-in Plugins supply the core plugin.
func EmployeeDefinitionInput(employeeID string) AgentDefinitionInput {
	return AgentDefinitionInput{
		Owners:       []string{employeeID},
		UsageScope:   UsageScope{Kind: EmployeeScopeKind, Reference: employeeID},
		Purpose:      EmployeeWorkPurpose,
		Capabilities: CapabilityConfiguration{StartupAgent: employeeStartupAgent},
	}
}
