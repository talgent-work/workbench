package domain

import "errors"

var (
	ErrRepositoryPreparationCanceled = errors.New("agent: repository preparation cancelled")
	ErrRepositoryPreparationConflict = errors.New("agent: repository preparation differs from the retained selection")
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
