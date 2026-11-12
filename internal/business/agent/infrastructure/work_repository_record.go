package infrastructure

import (
	"encoding/json"

	"github.com/samber/oops"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

type workRepositoryRecord struct {
	OwnerID      string                     `json:"owner_id"`
	WorkBranch   string                     `json:"work_branch,omitempty"`
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
	record := workRepositoryRecord{OwnerID: selection.OwnerID, WorkBranch: selection.WorkBranch}
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
	selection := domain.WorkRepositorySelection{OwnerID: record.OwnerID, WorkBranch: record.WorkBranch}
	for _, repo := range record.Repositories {
		selection.Repositories = append(selection.Repositories, domain.WorkRepository{
			Provider: repo.Provider, RepoID: repo.RepoID, RepoName: repo.RepoName,
			CloneURL: repo.CloneURL, WebURL: repo.WebURL, DefaultBranch: repo.DefaultBranch, BaseBranch: repo.BaseBranch,
		})
	}
	return selection, selection.Validate()
}

// repositoryCheckoutRecord is one selected repository's final checkout
// outcome.
type repositoryCheckoutRecord struct {
	RepositoryID string `json:"repository_id"`
	CheckedOut   bool   `json:"checked_out"`
	Reason       string `json:"reason,omitempty"`
}

func encodeRepositoryCheckouts(checkouts []domain.RepositoryCheckout) (string, error) {
	records := make([]repositoryCheckoutRecord, 0, len(checkouts))
	for _, checkout := range checkouts {
		records = append(records, repositoryCheckoutRecord{RepositoryID: checkout.RepositoryID, CheckedOut: checkout.CheckedOut, Reason: checkout.Reason})
	}
	return encode(records)
}

// decodeRepositoryCheckouts reads the recorded outcomes; a Work recorded
// before outcomes were kept on it has none.
func decodeRepositoryCheckouts(data string) ([]domain.RepositoryCheckout, error) {
	if data == "" {
		return nil, nil
	}
	var records []repositoryCheckoutRecord
	if err := json.Unmarshal([]byte(data), &records); err != nil {
		return nil, oops.Wrap(err)
	}
	var checkouts []domain.RepositoryCheckout
	for _, record := range records {
		checkouts = append(checkouts, domain.RepositoryCheckout{RepositoryID: record.RepositoryID, CheckedOut: record.CheckedOut, Reason: record.Reason})
	}
	return checkouts, nil
}
