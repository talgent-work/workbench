package domain

import (
	"errors"
	"strings"
)

var (
	ErrRepositoryPreparationCanceled = errors.New("agent: repository preparation cancelled")
	ErrRepositoryPreparationConflict = errors.New("agent: repository preparation differs from retained choices")
)

// AdmitRepositoryAccess decides whether the Work's current start receives
// temporary access to its selected repositories for ownerUserID: only while
// it holds an active process, never once it entered Closing (Repository
// preparation cancellation), and only for the user whose selection it
// retained (Repository preparation admission).
func (s *AgentWork) AdmitRepositoryAccess(ownerUserID string) error {
	if s.Closed() {
		return ErrRepositoryPreparationCanceled
	}
	if err := s.AuthorizeCredentialAccess(); err != nil {
		return err
	}
	selection := s.RepositorySelection
	if err := selection.Validate(); err != nil {
		return err
	}
	if len(selection.Repositories) > 0 && ownerUserID != selection.OwnerUserID {
		return ErrRepositoryPreparationConflict
	}
	return nil
}

// AdmitRepositoryChoices decides whether a repeated request's repository
// choices are the ones the Work retained: checkout always follows the
// retained selection, which a repeated request never changes (Repository
// preparation continuity). An omitted base branch names the retained one.
func (s *AgentWork) AdmitRepositoryChoices(ownerUserID string, choices []WorkRepositoryChoice) error {
	selection := s.RepositorySelection
	if len(choices) != len(selection.Repositories) || (len(choices) > 0 && ownerUserID != selection.OwnerUserID) {
		return ErrRepositoryPreparationConflict
	}
	for n, repo := range selection.Repositories {
		branch := strings.TrimSpace(choices[n].BaseBranch)
		if choices[n].RepositoryID != repo.RepoID || (branch != "" && branch != repo.BaseBranch) {
			return ErrRepositoryPreparationConflict
		}
	}
	return nil
}
