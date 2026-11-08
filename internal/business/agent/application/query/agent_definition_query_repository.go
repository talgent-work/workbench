package query

import "context"

// AgentDefinitionQueryRepository reads AgentDefinitions without loading
// them: the one serving a purpose, and current appearances.
type AgentDefinitionQueryRepository interface {
	FindForPurpose(ctx context.Context, selection GetAgentDefinitionForPurpose) (string, error)
	// ListEmployeeAgents reads the appearances of several employees' agents
	// for the purpose at once; an employee without a definition for it is
	// absent.
	ListEmployeeAgents(ctx context.Context, purpose string, employeeIDs []string) (map[string]AgentAppearanceView, error)
	// ListWorkAgents reads the appearances of several Works' agents at once;
	// an unknown Work is absent.
	ListWorkAgents(ctx context.Context, workIDs []string) (map[string]AgentAppearanceView, error)
}
