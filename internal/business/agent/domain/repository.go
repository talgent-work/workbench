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
	// FindBySubject returns the definition's Work of the Subject, a closed one
	// included, or nil when there is none.
	FindBySubject(ctx context.Context, agentID string, subject WorkSubject) (*AgentWork, error)
	Save(context.Context, *AgentWork) error
}

// AgentExecutionRepository holds every AgentExecution.
type AgentExecutionRepository interface {
	Get(context.Context, string) (*AgentExecution, error)
	// Latest returns the Work's most recently begun execution, answering
	// ErrExecutionNotFound when it has none.
	Latest(ctx context.Context, workID string) (*AgentExecution, error)
	// Save inserts a new execution, rejecting a second unretired one of its
	// Work with ErrExecutionAdmission, or updates a loaded one, answering
	// ErrConcurrentModification when it changed since.
	Save(context.Context, *AgentExecution) error
}
