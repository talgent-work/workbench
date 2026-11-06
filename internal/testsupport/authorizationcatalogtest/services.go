package authorizationcatalogtest

import (
	"context"

	"github.com/talgent-work/workbench/internal/business/agent/application"
)

// NoServices keeps existing environment lifecycle scenarios independent of
// external-provider supply. Credential acceptance tests use the real use case.
type NoServices struct{}

func (NoServices) Resolve(context.Context, string, string, []application.ServiceRequirement) (application.ServiceAccessResult, error) {
	return application.ServiceAccessResult{Unavailable: []application.UnavailableService{{Service: "github", Target: "github-api", Reason: "not_selected"}}}, nil
}
