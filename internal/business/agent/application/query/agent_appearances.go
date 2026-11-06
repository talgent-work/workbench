package query

import (
	"context"
	"strings"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// MaxResolvedAgents bounds one request; callers asking about more employees or
// Works split them into requests of at most this many.
const MaxResolvedAgents = 200

// AgentAppearanceView is one definition's current appearance, as read.
type AgentAppearanceView struct {
	AgentID    string
	Appearance domain.AgentAppearance
}

// ResolvedAgent answers one requested employee or Work; an unknown one has
// no Agent.
type ResolvedAgent struct {
	Reference string
	Known     bool
	AgentAppearanceView
}

// AgentAppearanceReader reads current appearances at once; an employee
// without a definition for the purpose, or an unknown Work, is absent.
type AgentAppearanceReader interface {
	ListEmployeeAgents(ctx context.Context, purpose string, employeeIDs []string) (map[string]AgentAppearanceView, error)
	ListWorkAgents(ctx context.Context, workIDs []string) (map[string]AgentAppearanceView, error)
}

// ResolveAgentsHandler answers in one read so a caller showing many Agents
// does not ask one by one.
type ResolveAgentsHandler struct {
	agents AgentAppearanceReader
}

func NewResolveAgentsHandler(agents AgentAppearanceReader) *ResolveAgentsHandler {
	return &ResolveAgentsHandler{agents: agents}
}

// ByEmployee resolves the employees' definitions for the caller's purpose,
// the work Agents that represent them; Agent does not interpret the purpose.
func (h *ResolveAgentsHandler) ByEmployee(ctx context.Context, purpose string, employeeIDs []string) ([]ResolvedAgent, error) {
	purpose = strings.TrimSpace(purpose)
	if purpose == "" {
		return nil, ErrInvalidDefinitionSelection
	}
	return resolveAgents(ctx, employeeIDs, func(ctx context.Context, ids []string) (map[string]AgentAppearanceView, error) {
		return h.agents.ListEmployeeAgents(ctx, purpose, ids)
	})
}

// OfEmployee reads the employee's work Agent, or, when it has none yet, the
// appearance it would be established with; AgentID is then empty.
func (h *ResolveAgentsHandler) OfEmployee(ctx context.Context, purpose, employeeID, displayName string) (AgentAppearanceView, error) {
	employeeID, purpose = strings.TrimSpace(employeeID), strings.TrimSpace(purpose)
	if employeeID == "" || purpose == "" {
		return AgentAppearanceView{}, ErrInvalidDefinitionSelection
	}
	agents, err := h.agents.ListEmployeeAgents(ctx, purpose, []string{employeeID})
	if err != nil {
		return AgentAppearanceView{}, err
	}
	if agent, found := agents[employeeID]; found {
		return agent, nil
	}
	return AgentAppearanceView{Appearance: domain.DefaultEmployeeAppearance(displayName)}, nil
}

// ByWork resolves the Agents of the Works.
func (h *ResolveAgentsHandler) ByWork(ctx context.Context, workIDs []string) ([]ResolvedAgent, error) {
	return resolveAgents(ctx, workIDs, h.agents.ListWorkAgents)
}

func resolveAgents(ctx context.Context, references []string, read func(context.Context, []string) (map[string]AgentAppearanceView, error)) ([]ResolvedAgent, error) {
	if len(references) > MaxResolvedAgents {
		return nil, ErrInvalidDefinitionSelection
	}
	ids := make([]string, 0, len(references))
	for _, reference := range references {
		if reference = strings.TrimSpace(reference); reference != "" {
			ids = append(ids, reference)
		}
	}
	agents, err := read(ctx, ids)
	if err != nil {
		return nil, err
	}
	results := make([]ResolvedAgent, 0, len(references))
	for _, reference := range references {
		result := ResolvedAgent{Reference: reference}
		result.AgentAppearanceView, result.Known = agents[strings.TrimSpace(reference)]
		results = append(results, result)
	}
	return results, nil
}
