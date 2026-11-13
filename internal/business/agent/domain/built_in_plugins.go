package domain

import (
	"fmt"
	"slices"
)

// BuiltInPlugins are the Required Plugins the deployment declares by scope:
// the platform scope's for every Work, and each Work Caller kind's scope for
// every Work with a Work Caller of that kind. A Work composes them at creation
// and never reapplies a later change of them.
type BuiltInPlugins struct {
	platform []string
	kinds    map[string][]string
}

// NewBuiltInPlugins admits the platform scope's slugs and each Work Caller
// kind's slugs, each a valid Plugin name named once within its scope, under a
// valid kind.
func NewBuiltInPlugins(platform []string, kinds map[string][]string) (BuiltInPlugins, error) {
	if err := validateRequiredPlugins(platform); err != nil {
		return BuiltInPlugins{}, err
	}
	declared := make(map[string][]string, len(kinds))
	for kind, slugs := range kinds {
		if err := validateKind(kind, ErrInvalidWorkCaller); err != nil {
			return BuiltInPlugins{}, err
		}
		if err := validateRequiredPlugins(slugs); err != nil {
			return BuiltInPlugins{}, fmt.Errorf("work caller kind %q: %w", kind, err)
		}
		declared[kind] = slices.Clone(slugs)
	}
	return BuiltInPlugins{platform: slices.Clone(platform), kinds: declared}, nil
}

// Required are a new Work's Built-in Plugins: the platform scope's, then
// those declared for its Work Caller's kind, each once.
func (b BuiltInPlugins) Required(caller WorkCaller) []string {
	return WithRequiredPlugins(b.platform, b.kinds[caller.Kind])
}

// ComposeRequiredPlugins are a new Work's Required Plugins: the Built-in
// Plugins of its scopes, then its definition's, then its creating caller's,
// each once.
func (b BuiltInPlugins) ComposeRequiredPlugins(workCaller WorkCaller, definition, extension []string) []string {
	return WithRequiredPlugins(WithRequiredPlugins(b.Required(workCaller), definition), extension)
}
