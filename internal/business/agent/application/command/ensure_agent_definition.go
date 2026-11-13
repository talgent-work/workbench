package command

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"slices"

	"github.com/lzap/deagon"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

type EnsureAgentDefinition struct {
	domain.AgentDefinitionInput
	EmployeeDisplayName string
}

type InitializedAgentDefinition struct {
	AgentID    string
	Appearance domain.AgentAppearance
	Owners     []string
	// OwnerWorkCaller is the Work Caller of the definition's Standalone
	// Works, its owner; zero for a definition that starts none.
	OwnerWorkCaller domain.WorkCaller
}

type EnsureAgentDefinitionHandler struct {
	agents domain.AgentDefinitionRepository
	lookup query.AgentDefinitionQueryRepository
}

func NewEnsureAgentDefinitionHandler(agents domain.AgentDefinitionRepository, lookup query.AgentDefinitionQueryRepository) *EnsureAgentDefinitionHandler {
	return &EnsureAgentDefinitionHandler{agents: agents, lookup: lookup}
}

func (h *EnsureAgentDefinitionHandler) Handle(ctx context.Context, input EnsureAgentDefinition) (InitializedAgentDefinition, error) {
	if err := input.UsageScope.Validate(); err != nil {
		return InitializedAgentDefinition{}, err
	}
	for range 8 {
		if err := ctx.Err(); err != nil {
			return InitializedAgentDefinition{}, err
		}
		id, err := h.lookup.FindForPurpose(ctx, query.GetAgentDefinitionForPurpose{ScopeKind: input.UsageScope.Kind, ScopeReference: input.UsageScope.Reference, Purpose: input.Purpose})
		var agent *domain.AgentDefinition
		if errors.Is(err, domain.ErrAgentDefinitionNotFound) {
			agent, err = domain.NewAgentDefinition(input.AgentDefinitionInput)
		} else if err == nil {
			agent, err = h.agents.Get(ctx, id)
		}
		if err != nil {
			return InitializedAgentDefinition{}, err
		}
		// A located definition keeps its appearance; an unnamed project
		// definition is supplied a random initial name.
		projectInitialName := ""
		if agent.AgentName == "" && !agent.RepresentsHuman() {
			projectInitialName, err = randomAgentName()
		}
		changed := false
		if err == nil {
			changed, err = agent.EstablishAppearance(input.EmployeeDisplayName, projectInitialName)
		}
		if err != nil {
			return InitializedAgentDefinition{}, err
		}
		if changed || agent.Version == 0 {
			if err = h.agents.Save(ctx, agent); errors.Is(err, domain.ErrConcurrentModification) {
				continue
			}
			if err != nil {
				return InitializedAgentDefinition{}, err
			}
		}
		owner, _ := agent.OwnerWorkCaller()
		return InitializedAgentDefinition{AgentID: agent.ID, Appearance: agent.Appearance(), Owners: slices.Clone(agent.Owners), OwnerWorkCaller: owner}, nil
	}
	return InitializedAgentDefinition{}, domain.ErrConcurrentModification
}

func randomAgentName() (string, error) {
	var entropy [4]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return "", err
	}
	return deagon.Name(deagon.NewCapitalizedSpaceFormatter(), int(binary.BigEndian.Uint32(entropy[:])&0x01ffffff)), nil
}

// ChangeAgentAppearance carries the trusted requester and the chosen
// appearance.
type ChangeAgentAppearance struct {
	AgentID, Name string
	Glyph, Color  string
	Requester     domain.DefinitionRequester
}

type ChangeAgentAppearanceHandler struct {
	agents domain.AgentDefinitionRepository
}

func NewChangeAgentAppearanceHandler(agents domain.AgentDefinitionRepository) *ChangeAgentAppearanceHandler {
	return &ChangeAgentAppearanceHandler{agents: agents}
}

func (h *ChangeAgentAppearanceHandler) Handle(ctx context.Context, input ChangeAgentAppearance) (domain.AgentAppearance, error) {
	icon, err := domain.NewAgentIcon(input.Glyph, input.Color)
	if err != nil {
		return domain.AgentAppearance{}, err
	}
	for range 8 {
		if err := ctx.Err(); err != nil {
			return domain.AgentAppearance{}, err
		}
		agent, err := h.agents.Get(ctx, input.AgentID)
		if err != nil {
			return domain.AgentAppearance{}, err
		}
		changed, err := agent.ChangeAppearance(input.Requester, input.Name, icon)
		if err != nil || !changed {
			return agent.Appearance(), err
		}
		if err = h.agents.Save(ctx, agent); errors.Is(err, domain.ErrConcurrentModification) {
			continue
		}
		return agent.Appearance(), err
	}
	return domain.AgentAppearance{}, domain.ErrConcurrentModification
}
