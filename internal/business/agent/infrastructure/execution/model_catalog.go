package execution

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"gopkg.in/yaml.v3"
)

// Names that carry a credential, such as a vendor's own MOONSHOT_API_KEY; a
// template's settings are visible in the sandbox and never hold one.
var credentialName = regexp.MustCompile(`(?i)(^|_)(KEY|TOKEN|SECRET|PASSWORD|CREDENTIALS?)$`)

// Model selection and credentials are derived from the Model Configuration;
// a template's extra Harness settings cannot override them.
var modelManagedEnvironment = []string{
	"ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY", "ANTHROPIC_MODEL",
	"ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL", "ANTHROPIC_DEFAULT_FABLE_MODEL",
	"CLAUDE_CODE_SUBAGENT_MODEL", "CLAUDE_CODE_EFFORT_LEVEL",
}

// ModelTemplate describes how the Harness reaches one Model Vendor's models.
// It is platform data from a ConfigMap; a changed model is a new template.
type ModelTemplate struct {
	ID      string `yaml:"id"`
	Vendor  string `yaml:"vendor"`
	BaseURL string `yaml:"base-url"`
	// Claude Code's model tiers and subagent model, as the vendor's
	// documentation maps them. MainModel is optional: when set, such as a
	// vendor's routing alias "auto", the Work starts on it; when empty, Claude
	// Code picks its own default tier.
	MainModel            string            `yaml:"main-model"`
	OpusModel            string            `yaml:"opus-model"`
	SonnetModel          string            `yaml:"sonnet-model"`
	HaikuModel           string            `yaml:"haiku-model"`
	FableModel           string            `yaml:"fable-model"`
	SubagentModel        string            `yaml:"subagent-model"`
	ThinkingLevels       []string          `yaml:"thinking-levels"`
	DefaultThinkingLevel string            `yaml:"default-thinking-level"`
	Environment          map[string]string `yaml:"environment"`
}

// ModelConfiguration is one selectable model choice. Its APIKey comes from a
// Secret and is only written to a sandbox's credential Vault.
type ModelConfiguration struct {
	ID       string `yaml:"id"`
	Name     string `yaml:"name"`
	Template string `yaml:"template"`
	APIKey   string `yaml:"api-key"`
}

// ProvidedModel is a Model Configuration resolved to the template it names.
type ProvidedModel struct {
	Configuration ModelConfiguration
	Template      ModelTemplate
}

// errModelConfigurationNotProvided reports that an admitted start names a Model
// Configuration the catalog no longer provides, so no sandbox can expand it.
var errModelConfigurationNotProvided = errors.New("agent: the Work's model configuration is not provided")

// ModelCatalog holds the Model Configurations the platform currently provides,
// in platform order, each resolved to its Model Template.
type ModelCatalog struct {
	provided []ProvidedModel
}

// ModelCatalogConfig names the mounted template and configuration files.
type ModelCatalogConfig struct {
	TemplatesPath      string `json:"templates-path" yaml:"templates-path" toml:"templates-path"`
	ConfigurationsPath string `json:"configurations-path" yaml:"configurations-path" toml:"configurations-path"`
}

// LoadModelCatalog reads and validates the mounted files once at startup;
// changes take effect when the process restarts.
func LoadModelCatalog(cfg ModelCatalogConfig) (*ModelCatalog, error) {
	var templates struct {
		Templates []ModelTemplate `yaml:"templates"`
	}
	if err := decodeStrict(cfg.TemplatesPath, &templates); err != nil {
		return nil, fmt.Errorf("agent: model templates: %w", err)
	}
	var configurations struct {
		Configurations []ModelConfiguration `yaml:"configurations"`
	}
	// The Secret's decode error may quote its content, so it is not wrapped.
	if err := decodeStrict(cfg.ConfigurationsPath, &configurations); err != nil {
		return nil, errors.New("agent: model configurations cannot be read as YAML")
	}
	return NewModelCatalog(templates.Templates, configurations.Configurations)
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

// NewModelCatalog validates the platform data. Errors name the offending
// template or configuration and never include a key.
func NewModelCatalog(templates []ModelTemplate, configurations []ModelConfiguration) (*ModelCatalog, error) {
	byID := make(map[string]ModelTemplate, len(templates))
	for _, template := range templates {
		if err := template.validate(); err != nil {
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
	catalog := &ModelCatalog{}
	for _, configuration := range configurations {
		if (domain.WorkModelSelection{ConfigurationID: configuration.ID}).Validate() != nil {
			return nil, fmt.Errorf("agent: model configuration id %q is invalid", configuration.ID)
		}
		if _, found := catalog.Resolve(configuration.ID); found {
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
		catalog.provided = append(catalog.provided, ProvidedModel{Configuration: configuration, Template: template})
	}
	return catalog, nil
}

func (t ModelTemplate) validate() error {
	if !validIdentifier(t.ID) {
		return fmt.Errorf("agent: model template id %q is invalid", t.ID)
	}
	if !slices.Contains(query.ModelVendors, t.Vendor) {
		return fmt.Errorf("agent: model template %q has unknown vendor %q", t.ID, t.Vendor)
	}
	if _, err := credentialURL(t.BaseURL); err != nil {
		return fmt.Errorf("agent: model template %q: %w", t.ID, err)
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
	for key := range t.Environment {
		if slices.Contains(modelManagedEnvironment, key) || slices.Contains(sandboxSecretEnvironment, key) || credentialName.MatchString(key) {
			return fmt.Errorf("agent: model template %q cannot set %s", t.ID, key)
		}
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

// Provided lists the Model Configurations in platform order.
func (c *ModelCatalog) Provided() []ProvidedModel {
	return slices.Clone(c.provided)
}

// Resolve finds a provided Model Configuration; a withdrawn one is absent.
func (c *ModelCatalog) Resolve(configurationID string) (ProvidedModel, bool) {
	for _, model := range c.provided {
		if model.Configuration.ID == configurationID {
			return model, true
		}
	}
	return ProvidedModel{}, false
}
