package agent

import (
	"fmt"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// BuiltInPluginsConfig declares, by slug, the Plugins every Work of a scope
// requires: the platform scope's every Work, the Project scope's every Work
// of a Project. Neither a definition, a creating caller nor a person removes
// them.
type BuiltInPluginsConfig struct {
	Platform []string `json:"platform" toml:"platform" yaml:"platform"`
	Project  []string `json:"project" toml:"project" yaml:"project"`
}

func (c BuiltInPluginsConfig) Validate() error {
	_, err := newBuiltInPlugins(c)
	return err
}

func newBuiltInPlugins(c BuiltInPluginsConfig) (domain.BuiltInPlugins, error) {
	builtIns, err := domain.NewBuiltInPlugins(c.Platform, c.Project)
	if err != nil {
		return domain.BuiltInPlugins{}, fmt.Errorf("each scope names valid plugin slugs once: %w", err)
	}
	return builtIns, nil
}
