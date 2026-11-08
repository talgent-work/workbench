package execution

import (
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/query/querytest"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure/platformcatalog"
)

const fixtureModelKey = "model-token-fixture"

// fixtureModels provides the package's fixture configurations through one
// template whose endpoint the fixture Vault binds.
func fixtureModels() *platformcatalog.Catalog {
	template := command.ModelTemplate{ID: "fixture-template", Vendor: "deepseek", BaseURL: "https://model.example/anthropic", OpusModel: "fixture-primary", SonnetModel: "fixture-primary", HaikuModel: "fixture-lightweight", FableModel: "fixture-primary", SubagentModel: "fixture-lightweight", ThinkingLevels: []string{"low", "high", "max"}, DefaultThinkingLevel: "max"}
	var configurations []command.ModelConfiguration
	for _, model := range querytest.ProvidedModels().Provided() {
		configurations = append(configurations, command.ModelConfiguration{ID: model.Selection.ConfigurationID, Name: model.Selection.ConfigurationID, Template: template.ID, APIKey: fixtureModelKey})
	}
	return catalog([]command.ModelTemplate{template}, configurations...)
}

// catalog provides configurations through templates.
func catalog(templates []command.ModelTemplate, configurations ...command.ModelConfiguration) *platformcatalog.Catalog {
	models, err := platformcatalog.NewCatalog(templates, configurations)
	if err != nil {
		panic(err)
	}
	return models
}
