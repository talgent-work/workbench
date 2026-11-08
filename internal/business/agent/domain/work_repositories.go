package domain

import (
	"fmt"
	"slices"
	"strings"
)

// AccessibleRepository is an owner-scoped Marketplace fact: a repository the
// owner's current authorization reaches. It is not evidence of continuing access.
type AccessibleRepository struct {
	OwnerUserID   string `validate:"required,max=255"`
	Provider      string `validate:"required,max=64"`
	RepoID        string `validate:"required,max=255"`
	RepoName      string `validate:"required,max=512"`
	CloneURL      string `validate:"required"`
	WebURL        string
	DefaultBranch string `validate:"required,max=255"`
}

type WorkRepositoryChoice struct {
	RepositoryID string
	BaseBranch   string
}

type WorkRepositoryInput struct {
	OwnerUserID string
	// WorkBranch is the branch the creating caller names for the checkouts.
	WorkBranch string
	Choices    []WorkRepositoryChoice
	Available  []AccessibleRepository
}

// WorkRepository retains the selected external identity and branch. Concrete
// checkout paths and temporary access belong to preparation, not this value.
type WorkRepository struct {
	Provider      string `validate:"required,max=64"`
	RepoID        string `validate:"required,max=255"`
	RepoName      string `validate:"required,max=512"`
	CloneURL      string `validate:"required"`
	WebURL        string
	DefaultBranch string `validate:"required,max=255"`
	BaseBranch    string `validate:"required,max=255"`
}

type WorkRepositorySelection struct {
	OwnerUserID string `validate:"max=255"`
	// WorkBranch is the branch every selected repository is checked out on,
	// named by the creating caller.
	WorkBranch   string `validate:"max=255"`
	Repositories []WorkRepository
}

func (s *AgentWork) AuthorizeCredentialAccess() error {
	if !s.HoldsActiveProcess() {
		return ErrWorkTransition
	}
	return nil
}

// RepositoryAuthorization selects only the reference retained for the selected
// repository's supported service. Current validity belongs to Marketplace.
func (s *AgentWork) RepositoryAuthorization(repositoryID string) (WorkServiceAuthorization, error) {
	if err := s.AuthorizeCredentialAccess(); err != nil {
		return WorkServiceAuthorization{}, err
	}
	for _, repo := range s.RepositorySelection.Repositories {
		if repo.RepoID != repositoryID {
			continue
		}
		if repo.Provider != "github" {
			return WorkServiceAuthorization{}, ErrServiceAuthorizationNotSelected
		}
		ref, err := s.ServiceAuthorization(s.RepositorySelection.OwnerUserID, "github")
		if err != nil {
			return WorkServiceAuthorization{}, err
		}
		if ref.Provider != "github" {
			return WorkServiceAuthorization{}, ErrServiceAuthorizationNotSelected
		}
		return ref, nil
	}
	return WorkServiceAuthorization{}, ErrInvalidWork
}

func (s WorkRepositorySelection) Clone() WorkRepositorySelection {
	s.Repositories = slices.Clone(s.Repositories)
	return s
}

func (s WorkRepositorySelection) Validate() error {
	if err := validation.Struct(s); err != nil {
		return fmt.Errorf("%w: invalid repository owner", ErrInvalidWork)
	}
	selected := make(map[string]bool, len(s.Repositories))
	for _, repo := range s.Repositories {
		if err := validation.Struct(repo); err != nil || s.OwnerUserID == "" || selected[repo.RepoID] {
			return fmt.Errorf("%w: invalid repository selection", ErrInvalidWork)
		}
		selected[repo.RepoID] = true
	}
	return nil
}

func (s *AgentWork) determineRepositorySelection(input WorkRepositoryInput) error {
	if input.OwnerUserID != "" && input.OwnerUserID != s.RepresentedPerson {
		return fmt.Errorf("%w: repository owner differs from represented owner", ErrInvalidWork)
	}
	facts := make(map[string]AccessibleRepository, len(input.Available))
	for _, fact := range input.Available {
		if err := validation.Struct(fact); err != nil || fact.OwnerUserID != input.OwnerUserID {
			return fmt.Errorf("%w: repository facts are not owner scoped", ErrInvalidWork)
		}
		if _, exists := facts[fact.RepoID]; exists {
			return fmt.Errorf("%w: ambiguous repository fact", ErrInvalidWork)
		}
		facts[fact.RepoID] = fact
	}
	selection := WorkRepositorySelection{}
	for _, choice := range input.Choices {
		fact, found := facts[choice.RepositoryID]
		if !found || input.OwnerUserID == "" {
			return fmt.Errorf("%w: repository is not reachable under this owner's authorization", ErrInvalidWork)
		}
		base := strings.TrimSpace(choice.BaseBranch)
		if base == "" {
			base = fact.DefaultBranch
		}
		selection.OwnerUserID = input.OwnerUserID
		selection.Repositories = append(selection.Repositories, WorkRepository{
			Provider: fact.Provider, RepoID: fact.RepoID, RepoName: fact.RepoName,
			CloneURL: fact.CloneURL, WebURL: fact.WebURL, DefaultBranch: fact.DefaultBranch, BaseBranch: base,
		})
	}
	if len(selection.Repositories) > 0 {
		selection.WorkBranch = strings.TrimSpace(input.WorkBranch)
		if selection.WorkBranch == "" {
			return fmt.Errorf("%w: repository checkouts need a work branch", ErrInvalidWork)
		}
	}
	if err := selection.Validate(); err != nil {
		return err
	}
	s.RepositorySelection = selection
	return nil
}
