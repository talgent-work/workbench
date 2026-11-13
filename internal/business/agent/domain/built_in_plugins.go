package domain

import (
	"slices"
)

// BuiltInPlugins are the Required Plugins the deployment declares by scope:
// the platform scope's for every Work, the extension scope's for every
// Extension Work. A Work composes them at creation and never reapplies a
// later change of them.
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
// extension scope's when its Work Caller is of an extension-defined kind,
// each once.
func (b BuiltInPlugins) Required(caller WorkCaller) []string {
	if !caller.ExtensionDefined() {
		return slices.Clone(b.platform)
	}
	return WithRequiredPlugins(b.platform, b.project)
}

// ComposeRequiredPlugins are a new Work's Required Plugins: the Built-in
// Plugins of its scopes, then its definition's, then its creating caller's,
// each once.
func (b BuiltInPlugins) ComposeRequiredPlugins(workCaller WorkCaller, definition, extension []string) []string {
	return WithRequiredPlugins(WithRequiredPlugins(b.Required(workCaller), definition), extension)
}
