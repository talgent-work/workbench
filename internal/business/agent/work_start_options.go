package agent

import (
	"slices"

	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure/execution"
)

// newModelConfigurations offers the provided Model Configurations in platform
// order, each presented by its name and its Model Template's vendor and
// thinking levels. No endpoint or credential leaves the catalog.
func newModelConfigurations(models *execution.ModelCatalog) (query.ModelConfigurations, error) {
	var options []query.WorkModelOption
	for _, model := range models.Provided() {
		selection := domain.WorkModelSelection{ConfigurationID: model.Configuration.ID}
		if err := selection.Validate(); err != nil {
			return query.ModelConfigurations{}, err
		}
		options = append(options, query.WorkModelOption{Selection: selection, Name: model.Configuration.Name, Vendor: model.Template.Vendor,
			ThinkingLevels: slices.Clone(model.Template.ThinkingLevels), DefaultThinkingLevel: model.Template.DefaultThinkingLevel})
	}
	return query.NewModelConfigurations(options...), nil
}

// Every Work view presents its retained Model Configuration as currently provided.
func newWorkViewReader(views *infrastructure.WorkQueryRepository, models query.ModelConfigurations) query.WorkViewReader {
	return query.NewModelOptionViews(views, models)
}
