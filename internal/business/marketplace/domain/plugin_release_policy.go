package domain

import (
	"errors"
	"github.com/talgent-work/workbench/internal/pkg/pluginconfig"
	"reflect"
	"strings"
)

var ErrInvalidRelease = errors.New("marketplace: incomplete synchronized release")
var ErrReleaseChanged = errors.New("marketplace: an existing release version cannot be replaced")

// PluginRelease is immutable publication content; its identity is source, slug
// and version, independently of storage identifiers. The version is the one
// upstream declares or, when none is declared, the one its package pin derives.
// It names a release and never orders releases.
type PluginRelease struct {
	Slug, Source, Version, Description, RetrievalRef string
	NativeEntry                                      []byte
	Skills                                           []SkillComponent
	Agents                                           []AgentComponent
	MCPServers                                       []MCPComponent
	Parameters                                       ParameterDeclarations
}
type SkillComponent struct{ Name, Description, ArgumentHint string }
type AgentComponent struct{ Name, Description string }
type MCPComponent struct {
	Name       string
	Definition []byte
}

type PluginReleasePolicy struct{}

func (PluginReleasePolicy) AcceptRelease(candidate PluginRelease, published *PluginRelease) error {
	if !pluginconfig.ValidPluginName(candidate.Slug) || strings.TrimSpace(candidate.Source) == "" || len(candidate.Source) > 128 || strings.TrimSpace(candidate.Version) == "" || len(candidate.Version) > 255 || candidate.RetrievalRef == "" || len(candidate.NativeEntry) == 0 {
		return ErrInvalidRelease
	}
	if err := candidate.Parameters.Validate(); err != nil {
		return err
	}
	if published != nil && !reflect.DeepEqual(candidate, *published) {
		return ErrReleaseChanged
	}
	return nil
}

// DetermineUpdateResult never substitutes another source's release on failure.
func (PluginReleasePolicy) DetermineUpdateResult(candidate, retained *PluginRelease, failure error) (*PluginRelease, error) {
	if failure != nil {
		return retained, failure
	}
	if candidate == nil {
		return retained, ErrInvalidRelease
	}
	return candidate, nil
}
