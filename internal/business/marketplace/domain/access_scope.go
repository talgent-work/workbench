package domain

import (
	"slices"
	"strings"
	"time"
)

// AccessScope is the last observed reach of a ProviderAuthorization: the
// accounts and repositories the Provider granted, and the permissions, when they
// were observed. It serves reads and choices and grants no access by itself; the
// Provider still decides each actual access.
type AccessScope struct {
	Accounts    []ScopeAccount
	Permissions []ScopePermission
	ObservedAt  time.Time
}

// ScopeAccount is one personal account or organization reached through one
// Provider installation.
type ScopeAccount struct {
	InstallationID                                           string
	Login, Type, AvatarURL, RepositorySelection, SettingsURL string
	Repositories                                             []ScopeRepository
}

type ScopeRepository struct {
	RepoID, RepoName, CloneURL, WebURL, DefaultBranch string
}

type ScopePermission struct{ Name, Access string }

// Observed reports whether the Provider's reach has ever been observed.
func (s AccessScope) Observed() bool { return !s.ObservedAt.IsZero() }

// Repositories is the union of every account's repositories, ordered by name.
func (s AccessScope) Repositories() []ScopeRepository {
	seen := map[string]bool{}
	var repositories []ScopeRepository
	for _, account := range s.Accounts {
		for _, repository := range account.Repositories {
			if !seen[repository.RepoID] {
				seen[repository.RepoID] = true
				repositories = append(repositories, repository)
			}
		}
	}
	slices.SortFunc(repositories, func(a, b ScopeRepository) int { return strings.Compare(a.RepoName, b.RepoName) })
	return repositories
}

// Repository finds a repository last observed as reachable.
func (s AccessScope) Repository(repoID string) (ScopeRepository, bool) {
	repoID = strings.TrimSpace(repoID)
	for _, account := range s.Accounts {
		for _, repository := range account.Repositories {
			if repository.RepoID == repoID {
				return repository, true
			}
		}
	}
	return ScopeRepository{}, false
}

// ObserveAccessScope replaces the retained scope with a verified observation of
// what the Provider currently grants. Only an Active authorization reaches
// anything; an observation that could not be obtained is never supplied, so the
// retained scope and Lifecycle State stay unchanged.
func (a *ProviderAuthorization) ObserveAccessScope(observed AccessScope) error {
	if observed.ObservedAt.IsZero() {
		return ErrInvalidRequiredField
	}
	if !a.IsActive() {
		return ErrInvalidTransition
	}
	if observed.ObservedAt.Before(a.AccessScope.ObservedAt) {
		return nil // a slower, older observation never replaces a newer one
	}
	observed.ObservedAt = observed.ObservedAt.UTC()
	a.AccessScope = observed
	return nil
}
