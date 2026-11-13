package agent

import (
	"fmt"
	"slices"
	"strings"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// BuiltInPluginsConfig declares, by slug, the Plugins every Work of a scope
// requires: the platform scope's every Work, and each Work Caller kind's,
// under work-caller-kinds, every Work started in the name of a Work Caller of
// that kind. Neither a definition, a creating extension nor a person removes
// them.
//
// A blank entry names no Plugin and is left out, so that an entry a
// deployment switch fills from its environment, such as
// "${AGENT_API_PLUGIN:talgent-api}", drops out when the switch empties it.
type BuiltInPluginsConfig struct {
	Platform        []string            `json:"platform" toml:"platform" yaml:"platform"`
	WorkCallerKinds map[string][]string `json:"work-caller-kinds" toml:"work-caller-kinds" yaml:"work-caller-kinds"`
}

func (c BuiltInPluginsConfig) Validate() error {
	_, err := c.Plugins()
	return err
}

// Plugins are the Built-in Plugins the configuration declares.
func (c BuiltInPluginsConfig) Plugins() (domain.BuiltInPlugins, error) {
	kinds := make(map[string][]string, len(c.WorkCallerKinds))
	for kind, slugs := range c.WorkCallerKinds {
		kinds[kind] = named(slugs)
	}
	builtIns, err := domain.NewBuiltInPlugins(named(c.Platform), kinds)
	if err != nil {
		return domain.BuiltInPlugins{}, fmt.Errorf("each scope names valid plugin slugs once under a valid work caller kind: %w", err)
	}
	return builtIns, nil
}

func named(slugs []string) []string {
	return slices.DeleteFunc(slices.Clone(slugs), func(slug string) bool { return strings.TrimSpace(slug) == "" })
}
