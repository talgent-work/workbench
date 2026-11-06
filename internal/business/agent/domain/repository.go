package domain

import (
	"context"
	"errors"
)

var (
	ErrAgentDefinitionNotFound = errors.New("agent: definition not found")
	ErrWorkNotFound            = errors.New("agent: work not found")
	ErrConcurrentModification  = errors.New("agent: concurrent modification")
)

type AgentDefinitionRepository interface {
	Get(context.Context, string) (*AgentDefinition, error)
	Save(context.Context, *AgentDefinition) error
}

type AgentWorkRepository interface {
	Get(context.Context, string) (*AgentWork, error)
	Save(context.Context, *AgentWork) error
}
