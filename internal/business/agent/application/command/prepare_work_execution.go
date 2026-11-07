package command

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// ErrInitialExecutionConflict refuses a first-start request whose repository
// choices differ from those its Work retained.
var ErrInitialExecutionConflict = errors.New("agent: conflicting initial execution request")

type RepositorySelection struct {
	RepositoryID string
	BaseBranch   string
}

// PrepareWorkExecution is a new Work's first start as its caller asks for
// it: the initial task input, the initial thinking level, and the
// repositories to check out on the branch the caller names. Creation commits
// all of it with the Work; nothing tracks how far the start got.
type PrepareWorkExecution struct {
	// Initial Harness setting, independent of the Session's fixed capabilities.
	ThinkingLevel string
	SubjectID     string
	// WorkID names an existing Work for a repeat outside creation.
	WorkID    string
	RequestID string
	// Branch the caller names for repository checkouts.
	WorkBranch string
	Input      ExecutionInput
	Selections []RepositorySelection
}

// repositoryChoices are the selections as the Work's repository choices.
func (p PrepareWorkExecution) repositoryChoices() []domain.WorkRepositoryChoice {
	choices := make([]domain.WorkRepositoryChoice, 0, len(p.Selections))
	for _, selection := range p.Selections {
		choices = append(choices, domain.WorkRepositoryChoice{RepositoryID: selection.RepositoryID, BaseBranch: selection.BaseBranch})
	}
	return choices
}

// PrepareWorkExecutionHandler answers a first-start request for a Work that
// already exists: the repeat of its creation, or a reuse of its Subject's
// Work. Initial execution is part of accepted creation, never a later
// transition, so it starts nothing: it confirms the Work when the request
// asks for what the Work retained (Repository preparation continuity).
type PrepareWorkExecutionHandler struct {
	models        query.ModelConfigurations
	sessions      domain.AgentWorkRepository
	authorization access.WorkAuthorizer
}

func NewPrepareWorkExecutionHandler(sessions domain.AgentWorkRepository, authorization access.WorkAuthorizer, models query.ModelConfigurations) *PrepareWorkExecutionHandler {
	return &PrepareWorkExecutionHandler{models: models, sessions: sessions, authorization: authorization}
}

func (h *PrepareWorkExecutionHandler) Handle(ctx context.Context, input PrepareWorkExecution) (string, error) {
	if err := h.authorization.AuthorizeWork(ctx, input.SubjectID, input.WorkID); err != nil {
		return "", err
	}
	if err := validateInitialPreparation(input); err != nil {
		return "", err
	}
	work, err := h.sessions.Get(ctx, input.WorkID)
	if err != nil {
		return "", err
	}
	if err := work.AdmitPreparation(); err != nil {
		return "", err
	}
	if err := validateInitialThinkingLevel(h.models, work.WorkModel, input.ThinkingLevel); err != nil {
		return "", err
	}
	if err := work.AdmitRepositoryChoices(input.SubjectID, input.repositoryChoices()); err != nil {
		return "", errors.Join(ErrInitialExecutionConflict, err)
	}
	return work.ID, nil
}

func validateInitialPreparation(input PrepareWorkExecution) error {
	if err := input.Input.Validate(); err != nil {
		return err
	}
	if uuid.Validate(input.RequestID) != nil {
		return domain.ErrInvalidWork
	}
	return nil
}
