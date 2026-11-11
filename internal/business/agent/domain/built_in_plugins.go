package domain

import (
	"slices"
	"strings"
)

// BuiltInPlugins are the Required Plugins the deployment declares by scope:
// the platform scope's for every Work, the Project scope's for every Work of
// a Project. A Work composes them at creation and never reapplies a later
// change of them.
type BuiltInPlugins struct {
	platform, project []string
}

// NewBuiltInPlugins admits each scope's slugs, each a valid Plugin name named
// once within its scope.
func NewBuiltInPlugins(platform, project []string) (BuiltInPlugins, error) {
	for _, scope := range [][]string{platform, project} {
		if err := validateRequiredPlugins(scope); err != nil {
			return BuiltInPlugins{}, err
		}
	}
	return BuiltInPlugins{platform: slices.Clone(platform), project: slices.Clone(project)}, nil
}

// Required are a new Work's Built-in Plugins: the platform scope's, and the
// Project scope's when the Work has a Project, each once.
func (b BuiltInPlugins) Required(projectID string) []string {
	if strings.TrimSpace(projectID) == "" {
		return slices.Clone(b.platform)
	}
	return WithRequiredPlugins(b.platform, b.project)
}

// ComposeRequiredPlugins are a new Work's Required Plugins: the Built-in
// Plugins of its scopes, then its definition's, then its creating caller's,
// each once.
func (b BuiltInPlugins) ComposeRequiredPlugins(projectID string, definition, caller []string) []string {
	return WithRequiredPlugins(WithRequiredPlugins(b.Required(projectID), definition), caller)
}
