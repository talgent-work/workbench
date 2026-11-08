package authorizationcatalogtest

import (
	"context"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
)

// NoServices keeps existing environment lifecycle scenarios independent of
// external-provider supply. Credential acceptance tests use the real use case.
type NoServices struct{}

var _ command.ServiceSupply = NoServices{}

func (NoServices) Resolve(context.Context, string, string, []command.ServiceRequirement) (command.ServiceAccessResult, error) {
	return command.ServiceAccessResult{Unavailable: []command.UnavailableService{{Service: "github", Target: "github-api", Reason: "not_selected"}}}, nil
}

// NoRepositories supplies a Work that selected no repository: an empty plan
// and nothing to resolve.
type NoRepositories struct{}

var _ command.RepositorySupply = NoRepositories{}

func (NoRepositories) Plan(context.Context, string, string) ([]command.RepositoryAccessReference, error) {
	return []command.RepositoryAccessReference{}, nil
}

func (NoRepositories) Resolve(context.Context, string, string) (command.RepositoryAccessResult, error) {
	return command.RepositoryAccessResult{}, nil
}
