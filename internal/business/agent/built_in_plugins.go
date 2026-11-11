package agent

import (
	"fmt"
	"slices"
	"strings"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// BuiltInPluginsConfig declares, by slug, the Plugins every Work of a scope
// requires: the platform scope's every Work, the Project scope's every Work
// of a Project. Neither a definition, a creating caller nor a person removes
// them.
//
// A blank entry names no Plugin and is left out, so that an entry a
// deployment switch fills from its environment, such as
// "${AGENT_API_PLUGIN:talgent-api}", drops out when the switch empties it.
type BuiltInPluginsConfig struct {
	Platform []string `json:"platform" toml:"platform" yaml:"platform"`
	Project  []string `json:"project" toml:"project" yaml:"project"`
}

func (c BuiltInPluginsConfig) Validate() error {
	_, err := c.Plugins()
	return err
}

// Plugins are the Built-in Plugins the configuration declares.
func (c BuiltInPluginsConfig) Plugins() (domain.BuiltInPlugins, error) {
	builtIns, err := domain.NewBuiltInPlugins(named(c.Platform), named(c.Project))
	if err != nil {
		return domain.BuiltInPlugins{}, fmt.Errorf("each scope names valid plugin slugs once: %w", err)
	}
	return builtIns, nil
}

func named(slugs []string) []string {
	return slices.DeleteFunc(slices.Clone(slugs), func(slug string) bool { return strings.TrimSpace(slug) == "" })
}
