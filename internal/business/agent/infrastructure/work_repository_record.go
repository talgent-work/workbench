package infrastructure

import (
	"encoding/json"

	"github.com/samber/oops"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

type workRepositoryRecord struct {
	OwnerUserID  string                     `json:"owner_user_id"`
	Repositories []selectedRepositoryRecord `json:"repositories"`
}

type selectedRepositoryRecord struct {
	Provider      string `json:"provider"`
	RepoID        string `json:"repo_id"`
	RepoName      string `json:"repo_name"`
	CloneURL      string `json:"clone_url"`
	WebURL        string `json:"web_url"`
	DefaultBranch string `json:"default_branch"`
	BaseBranch    string `json:"base_branch"`
}

func encodeWorkRepositories(selection domain.WorkRepositorySelection) (string, error) {
	record := workRepositoryRecord{OwnerUserID: selection.OwnerUserID}
	for _, repo := range selection.Repositories {
		record.Repositories = append(record.Repositories, selectedRepositoryRecord{
			Provider: repo.Provider, RepoID: repo.RepoID, RepoName: repo.RepoName,
			CloneURL: repo.CloneURL, WebURL: repo.WebURL, DefaultBranch: repo.DefaultBranch, BaseBranch: repo.BaseBranch,
		})
	}
	return encode(record)
}

func decodeWorkRepositories(data string) (domain.WorkRepositorySelection, error) {
	var record workRepositoryRecord
	if err := json.Unmarshal([]byte(data), &record); err != nil {
		return domain.WorkRepositorySelection{}, oops.Wrap(err)
	}
	selection := domain.WorkRepositorySelection{OwnerUserID: record.OwnerUserID}
	for _, repo := range record.Repositories {
		selection.Repositories = append(selection.Repositories, domain.WorkRepository{
			Provider: repo.Provider, RepoID: repo.RepoID, RepoName: repo.RepoName,
			CloneURL: repo.CloneURL, WebURL: repo.WebURL, DefaultBranch: repo.DefaultBranch, BaseBranch: repo.BaseBranch,
		})
	}
	return selection, selection.Validate()
}
