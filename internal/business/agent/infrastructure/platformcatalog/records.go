package platformcatalog

import "github.com/talgent-work/workbench/internal/business/agent/application/command"

// templateRecord is a Model Template as the mounted ConfigMap spells it.
type templateRecord struct {
	ID                   string            `yaml:"id"`
	Vendor               string            `yaml:"vendor"`
	BaseURL              string            `yaml:"base-url"`
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

func (r templateRecord) template() command.ModelTemplate {
	return command.ModelTemplate(r)
}

// configurationRecord is a Model Configuration as the mounted Secret spells
// it.
type configurationRecord struct {
	ID       string `yaml:"id"`
	Name     string `yaml:"name"`
	Template string `yaml:"template"`
	APIKey   string `yaml:"api-key"`
}
