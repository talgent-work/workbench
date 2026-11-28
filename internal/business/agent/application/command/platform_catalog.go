package command

// PlatformCatalogPort reads the platform data operators change without a
// release, from the deployment's configuration: the Model Configurations the
// platform provides, in platform order, each resolved to the Model Template
// it names.
type PlatformCatalogPort interface {
	// ProvidedModels lists the provided Model Configurations in platform
	// order; the first is the default.
	ProvidedModels() []ProvidedModel
	// ProvidedModel finds a provided Model Configuration; a withdrawn one is
	// absent.
	ProvidedModel(configurationID string) (ProvidedModel, bool)
}

// ModelTemplate describes how the Harness reaches one Model Vendor's models:
// through an endpoint with a model for each tier and subagents, or, naming
// neither, through the Harness's own defaults. A changed model is a new
// template.
type ModelTemplate struct {
	ID      string
	Vendor  string
	BaseURL string
	// Claude Code's model tiers and subagent model, as the vendor's
	// documentation maps them, named together with BaseURL. MainModel is
	// optional: when set, such as a vendor's routing alias "auto", the Work
	// starts on it; when empty, Claude Code picks its own default tier.
	MainModel            string
	OpusModel            string
	SonnetModel          string
	HaikuModel           string
	FableModel           string
	SubagentModel        string
	ThinkingLevels       []string
	DefaultThinkingLevel string
	// Environment holds the vendor's further Harness settings.
	Environment map[string]string
}

// NamesEndpoint reports whether the template names an endpoint and its
// models; one that does not leaves the Work to the Harness's default models
// and to whatever credential the Work's environment provides.
func (t ModelTemplate) NamesEndpoint() bool {
	return t.BaseURL != ""
}

// ModelConfiguration is one selectable model choice. It carries an APIKey
// exactly when its template names an endpoint, and the key is only ever
// written to a run's credential store.
type ModelConfiguration struct {
	ID       string
	Name     string
	Template string
	APIKey   string
}

// ProvidedModel is a Model Configuration resolved to the template it names.
type ProvidedModel struct {
	Configuration ModelConfiguration
	Template      ModelTemplate
}
