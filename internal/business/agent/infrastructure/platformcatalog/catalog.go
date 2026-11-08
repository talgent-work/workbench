// Package platformcatalog reads the platform data deployment configuration
// holds: the Model Templates and Model Configurations mounted from
// Kubernetes ConfigMaps and Secrets.
package platformcatalog

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// Config names the mounted template and configuration files.
type Config struct {
	TemplatesPath      string `json:"templates-path" yaml:"templates-path" toml:"templates-path"`
	ConfigurationsPath string `json:"configurations-path" yaml:"configurations-path" toml:"configurations-path"`
}

// Catalog holds the Model Configurations the platform currently provides,
// in platform order, each resolved to its Model Template.
type Catalog struct {
	provided []command.ProvidedModel
}

var _ command.PlatformCatalogPort = (*Catalog)(nil)

// Load reads and validates the mounted files once at startup; changes take
// effect when the process restarts.
func Load(cfg Config) (*Catalog, error) {
	var templates struct {
		Templates []templateRecord `yaml:"templates"`
	}
	if err := decodeStrict(cfg.TemplatesPath, &templates); err != nil {
		return nil, fmt.Errorf("agent: model templates: %w", err)
	}
	var configurations struct {
		Configurations []configurationRecord `yaml:"configurations"`
	}
	// The Secret's decode error may quote its content, so it is not wrapped.
	if err := decodeStrict(cfg.ConfigurationsPath, &configurations); err != nil {
		return nil, errors.New("agent: model configurations cannot be read as YAML")
	}
	mappedTemplates := make([]command.ModelTemplate, 0, len(templates.Templates))
	for _, template := range templates.Templates {
		mappedTemplates = append(mappedTemplates, template.template())
	}
	mappedConfigurations := make([]command.ModelConfiguration, 0, len(configurations.Configurations))
	for _, configuration := range configurations.Configurations {
		mappedConfigurations = append(mappedConfigurations, command.ModelConfiguration(configuration))
	}
	return NewCatalog(mappedTemplates, mappedConfigurations)
}

func decodeStrict(path string, target any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	return decoder.Decode(target)
}

// NewCatalog validates the platform data. Errors name the offending template
// or configuration and never include a key. What a template may set in a
// run's environment is the execution environment's to judge.
func NewCatalog(templates []command.ModelTemplate, configurations []command.ModelConfiguration) (*Catalog, error) {
	byID := make(map[string]command.ModelTemplate, len(templates))
	for _, template := range templates {
		if err := validateTemplate(template); err != nil {
			return nil, err
		}
		if _, duplicate := byID[template.ID]; duplicate {
			return nil, fmt.Errorf("agent: model template %q is declared twice", template.ID)
		}
		byID[template.ID] = template
	}
	if len(configurations) == 0 {
		return nil, errors.New("agent: at least one model configuration is required")
	}
	catalog := &Catalog{}
	for _, configuration := range configurations {
		if (domain.WorkModelSelection{ConfigurationID: configuration.ID}).Validate() != nil {
			return nil, fmt.Errorf("agent: model configuration id %q is invalid", configuration.ID)
		}
		if _, found := catalog.ProvidedModel(configuration.ID); found {
			return nil, fmt.Errorf("agent: model configuration %q is declared twice", configuration.ID)
		}
		if strings.TrimSpace(configuration.Name) == "" {
			return nil, fmt.Errorf("agent: model configuration %q needs a name", configuration.ID)
		}
		if strings.TrimSpace(configuration.APIKey) == "" || strings.ContainsAny(configuration.APIKey, "\x00\r\n") {
			return nil, fmt.Errorf("agent: model configuration %q needs an api key", configuration.ID)
		}
		template, ok := byID[configuration.Template]
		if !ok {
			return nil, fmt.Errorf("agent: model configuration %q uses unknown template %q", configuration.ID, configuration.Template)
		}
		catalog.provided = append(catalog.provided, command.ProvidedModel{Configuration: configuration, Template: template})
	}
	return catalog, nil
}

func validateTemplate(t command.ModelTemplate) error {
	if !validIdentifier(t.ID) {
		return fmt.Errorf("agent: model template id %q is invalid", t.ID)
	}
	if !slices.Contains(query.ModelVendors, t.Vendor) {
		return fmt.Errorf("agent: model template %q has unknown vendor %q", t.ID, t.Vendor)
	}
	for _, model := range []string{t.OpusModel, t.SonnetModel, t.HaikuModel, t.FableModel, t.SubagentModel} {
		if !validModelName(model) {
			return fmt.Errorf("agent: model template %q needs opus, sonnet, haiku, fable and subagent models", t.ID)
		}
	}
	if t.MainModel != "" && !validModelName(t.MainModel) {
		return fmt.Errorf("agent: model template %q main model is invalid", t.ID)
	}
	for i, level := range t.ThinkingLevels {
		if !slices.Contains(domain.ThinkingLevels, level) || slices.Contains(t.ThinkingLevels[:i], level) {
			return fmt.Errorf("agent: model template %q has invalid thinking level %q", t.ID, level)
		}
	}
	if t.DefaultThinkingLevel != "" && !slices.Contains(t.ThinkingLevels, t.DefaultThinkingLevel) {
		return fmt.Errorf("agent: model template %q default thinking level is not one of its levels", t.ID)
	}
	return nil
}

func validIdentifier(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '.' || r == '_') {
			return false
		}
	}
	return true
}

func validModelName(value string) bool {
	return strings.TrimSpace(value) == value && value != "" && len(value) <= 255 && !strings.ContainsAny(value, "\x00\r\n'\"`$\\")
}

// ProvidedModels lists the Model Configurations in platform order.
func (c *Catalog) ProvidedModels() []command.ProvidedModel {
	provided := make([]command.ProvidedModel, len(c.provided))
	for i, model := range c.provided {
		provided[i] = cloneModel(model)
	}
	return provided
}

// ProvidedModel finds a provided Model Configuration; a withdrawn one is
// absent.
func (c *Catalog) ProvidedModel(configurationID string) (command.ProvidedModel, bool) {
	for _, model := range c.provided {
		if model.Configuration.ID == configurationID {
			return cloneModel(model), true
		}
	}
	return command.ProvidedModel{}, false
}

// cloneModel copies a template's slices and maps, so no reader changes the
// catalog through them.
func cloneModel(model command.ProvidedModel) command.ProvidedModel {
	model.Template.ThinkingLevels = slices.Clone(model.Template.ThinkingLevels)
	model.Template.Environment = maps.Clone(model.Template.Environment)
	return model
}
