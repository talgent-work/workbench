package persistence

import (
	"encoding/json"

	"github.com/samber/oops"

	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

// accessScopeDO is the stored form of an AccessScope. InstallationIDs is
// denormalized so a Provider installation notification finds the
// authorizations that reached it.
type accessScopeDO struct {
	InstallationIDs []string            `json:"installation_ids"`
	Accounts        []scopeAccountDO    `json:"accounts"`
	Permissions     []scopePermissionDO `json:"permissions"`
}

type scopeAccountDO struct {
	InstallationID      string              `json:"installation_id"`
	Login               string              `json:"login"`
	Type                string              `json:"type"`
	AvatarURL           string              `json:"avatar_url"`
	RepositorySelection string              `json:"repository_selection"`
	SettingsURL         string              `json:"settings_url"`
	Repositories        []scopeRepositoryDO `json:"repositories"`
}

type scopeRepositoryDO struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	CloneURL      string `json:"clone_url"`
	WebURL        string `json:"web_url"`
	DefaultBranch string `json:"default_branch"`
}

type scopePermissionDO struct {
	Name   string `json:"name"`
	Access string `json:"access"`
}

func accessScopeToJSON(scope domain.AccessScope) (string, error) {
	do := accessScopeDO{InstallationIDs: []string{}, Accounts: []scopeAccountDO{}, Permissions: []scopePermissionDO{}}
	for _, account := range scope.Accounts {
		do.InstallationIDs = append(do.InstallationIDs, account.InstallationID)
		stored := scopeAccountDO{InstallationID: account.InstallationID, Login: account.Login, Type: account.Type, AvatarURL: account.AvatarURL,
			RepositorySelection: account.RepositorySelection, SettingsURL: account.SettingsURL, Repositories: []scopeRepositoryDO{}}
		for _, r := range account.Repositories {
			stored.Repositories = append(stored.Repositories, scopeRepositoryDO{ID: r.RepoID, Name: r.RepoName, CloneURL: r.CloneURL, WebURL: r.WebURL, DefaultBranch: r.DefaultBranch})
		}
		do.Accounts = append(do.Accounts, stored)
	}
	for _, p := range scope.Permissions {
		do.Permissions = append(do.Permissions, scopePermissionDO{Name: p.Name, Access: p.Access})
	}
	raw, err := json.Marshal(do)
	if err != nil {
		return "", oops.Wrap(err)
	}
	return string(raw), nil
}

// A row whose scope was never observed has no stored scope.
func accessScopeFromJSON(raw string, observedAtMillis int64) (domain.AccessScope, error) {
	scope := domain.AccessScope{ObservedAt: timeFromMillis(observedAtMillis)}
	if raw == "" {
		return scope, nil
	}
	var do accessScopeDO
	if err := json.Unmarshal([]byte(raw), &do); err != nil {
		return domain.AccessScope{}, oops.With("operation", "decode_access_scope").Wrap(err)
	}
	for _, account := range do.Accounts {
		restored := domain.ScopeAccount{InstallationID: account.InstallationID, Login: account.Login, Type: account.Type, AvatarURL: account.AvatarURL,
			RepositorySelection: account.RepositorySelection, SettingsURL: account.SettingsURL}
		for _, r := range account.Repositories {
			restored.Repositories = append(restored.Repositories, domain.ScopeRepository{RepoID: r.ID, RepoName: r.Name, CloneURL: r.CloneURL, WebURL: r.WebURL, DefaultBranch: r.DefaultBranch})
		}
		scope.Accounts = append(scope.Accounts, restored)
	}
	for _, p := range do.Permissions {
		scope.Permissions = append(scope.Permissions, domain.ScopePermission{Name: p.Name, Access: p.Access})
	}
	return scope, nil
}
