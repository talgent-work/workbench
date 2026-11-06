package domain

import (
	"errors"
	"strings"
)

var (
	ErrRepositoryPreparationCanceled = errors.New("agent: repository preparation cancelled")
	ErrRepositoryPreparationConflict = errors.New("agent: repository preparation differs from retained choices")
)

// RepositoryPreparation supplies request facts without making recovery records
// part of the aggregate. Concrete checkout paths remain Application data.
type RepositoryPreparation struct {
	OwnerUserID string
	Choices     []WorkRepositoryChoice
	Canceled    bool
}

func (s *AgentWork) AdmitRepositoryPreparation(facts RepositoryPreparation) error {
	if err := s.AdmitPreparation(); err != nil {
		return err
	}
	if facts.Canceled {
		return ErrRepositoryPreparationCanceled
	}
	if err := s.AuthorizeCredentialAccess(); err != nil {
		return err
	}
	selection := s.RepositorySelection
	if err := selection.Validate(); err != nil {
		return err
	}
	if len(facts.Choices) != len(selection.Repositories) || (len(selection.Repositories) > 0 && facts.OwnerUserID != selection.OwnerUserID) {
		return ErrRepositoryPreparationConflict
	}
	for n, repo := range selection.Repositories {
		choice := facts.Choices[n]
		branch := strings.TrimSpace(choice.BaseBranch)
		if choice.RepositoryID != repo.RepoID || (branch != "" && branch != repo.BaseBranch) {
			return ErrRepositoryPreparationConflict
		}
	}
	return nil
}
