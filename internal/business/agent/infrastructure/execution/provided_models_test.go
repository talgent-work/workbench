package execution

import "github.com/talgent-work/workbench/internal/business/agent/application/query/querytest"

const fixtureModelKey = "model-token-fixture"

// fixtureModels provides the package's fixture configurations through one
// template whose endpoint the fixture Vault binds.
func fixtureModels() *ModelCatalog {
	template := ModelTemplate{ID: "fixture-template", Vendor: "deepseek", BaseURL: "https://model.example/anthropic", OpusModel: "fixture-primary", SonnetModel: "fixture-primary", HaikuModel: "fixture-lightweight", FableModel: "fixture-primary", SubagentModel: "fixture-lightweight", ThinkingLevels: []string{"low", "high", "max"}, DefaultThinkingLevel: "max"}
	var configurations []ModelConfiguration
	for _, model := range querytest.ProvidedModels().Provided() {
		configurations = append(configurations, ModelConfiguration{ID: model.Selection.ConfigurationID, Name: model.Selection.ConfigurationID, Template: template.ID, APIKey: fixtureModelKey})
	}
	catalog, err := NewModelCatalog([]ModelTemplate{template}, configurations)
	if err != nil {
		panic(err)
	}
	return catalog
}
