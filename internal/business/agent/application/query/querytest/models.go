// Package querytest supplies Application query facts for tests.
package querytest

import (
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// ProvidedModels offers every Model Configuration the agent fixtures select,
// so their starts are admitted unless a test withdraws one.
func ProvidedModels() query.ModelConfigurations {
	var options []query.WorkModelOption
	for _, id := range []string{"default", "deployment-default", "fixture", "model", "deepseek-pro", "selected-model", "claude-sonnet-4-6", "native-model"} {
		options = append(options, query.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: id}})
	}
	return query.NewModelConfigurations(options...)
}
