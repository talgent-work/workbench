package query

import (
	"context"
	"errors"
	"strings"
)

var ErrInvalidDefinitionSelection = errors.New("agent: invalid internal definition selection")

type GetAgentDefinitionForPurpose struct {
	ScopeKind      string
	ScopeReference string
	Purpose        string
}

type GetAgentDefinitionForPurposeHandler struct {
	definitions AgentDefinitionQueryRepository
}

func NewGetAgentDefinitionForPurposeHandler(definitions AgentDefinitionQueryRepository) *GetAgentDefinitionForPurposeHandler {
	return &GetAgentDefinitionForPurposeHandler{definitions: definitions}
}

func (h *GetAgentDefinitionForPurposeHandler) Handle(ctx context.Context, input GetAgentDefinitionForPurpose) (string, error) {
	if strings.TrimSpace(input.ScopeKind) == "" || strings.TrimSpace(input.ScopeReference) == "" || strings.TrimSpace(input.Purpose) == "" {
		return "", ErrInvalidDefinitionSelection
	}
	return h.definitions.FindForPurpose(ctx, input)
}
