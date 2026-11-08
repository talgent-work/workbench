package agent

import (
	"slices"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"xorm.io/xorm"
)

// newModelConfigurations offers the provided Model Configurations in platform
// order, each presented by its name and its Model Template's vendor and
// thinking levels. No endpoint or credential leaves the catalog.
func newModelConfigurations(models command.PlatformCatalogPort) (query.ModelConfigurations, error) {
	var options []query.WorkModelOption
	for _, model := range models.ProvidedModels() {
		selection := domain.WorkModelSelection{ConfigurationID: model.Configuration.ID}
		if err := selection.Validate(); err != nil {
			return query.ModelConfigurations{}, err
		}
		options = append(options, query.WorkModelOption{Selection: selection, Name: model.Configuration.Name, Vendor: model.Template.Vendor,
			ThinkingLevels: slices.Clone(model.Template.ThinkingLevels), DefaultThinkingLevel: model.Template.DefaultThinkingLevel})
	}
	return query.NewModelConfigurations(options...), nil
}

// newWorkQueryRepository presents every Work view with its retained Model
// Configuration as currently provided.
func newWorkQueryRepository(db *xorm.Engine, models query.ModelConfigurations) query.WorkQueryRepository {
	return query.NewModelOptionViews(infrastructure.NewWorkQueryRepository(db), models)
}
