package domain

import (
	"errors"
	"slices"
	"strings"
	"unicode/utf8"
)

var (
	ErrInvalidAgentAppearance   = errors.New("agent: name must be 1 to 40 characters and the icon a preset glyph and color")
	ErrAgentAppearanceForbidden = errors.New("agent: only an Owner of a person's Work Agent, or an Application's Application Owner, changes its appearance")
)

const maxAgentNameRunes = 40

// Preset icon glyphs and colors. The web maps each id to its drawing.
var (
	agentIconGlyphs = []string{"bot", "sparkles", "brain", "rocket", "wand", "cat", "bird", "leaf", "flame", "star", "compass", "atom"}
	agentIconColors = []string{"slate", "blue", "violet", "green", "amber", "rose"}
)

// DefaultAgentIcon is the icon every definition starts with.
var DefaultAgentIcon = AgentIcon{Glyph: "bot", Color: "green"}

// AgentIcon is one preset glyph in one preset color.
type AgentIcon struct {
	Glyph string
	Color string
}

func NewAgentIcon(glyph, color string) (AgentIcon, error) {
	icon := AgentIcon{Glyph: strings.TrimSpace(glyph), Color: strings.TrimSpace(color)}
	if err := icon.Validate(); err != nil {
		return AgentIcon{}, err
	}
	return icon, nil
}

func (i AgentIcon) Validate() error {
	if !slices.Contains(agentIconGlyphs, i.Glyph) || !slices.Contains(agentIconColors, i.Color) {
		return ErrInvalidAgentAppearance
	}
	return nil
}

// AgentAppearance is how an Agent is shown wherever it appears.
type AgentAppearance struct {
	Name string
	Icon AgentIcon
}

// EmployeeDefaultAgentName names a Work Agent after the human it represents,
// shortening a long display name so the default is itself a valid name.
func EmployeeDefaultAgentName(displayName string) string {
	const suffix = "'s Agent"
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		return "Work Agent"
	}
	if room := maxAgentNameRunes - utf8.RuneCountInString(suffix); utf8.RuneCountInString(displayName) > room {
		displayName = strings.TrimSpace(string([]rune(displayName)[:room]))
	}
	return displayName + suffix
}

// DefaultEmployeeAppearance is the appearance a Work Agent is established with.
func DefaultEmployeeAppearance(displayName string) AgentAppearance {
	return AgentAppearance{Name: EmployeeDefaultAgentName(displayName), Icon: DefaultAgentIcon}
}

// Appearance reports the current name and icon.
func (a *AgentDefinition) Appearance() AgentAppearance {
	return AgentAppearance{Name: a.AgentName, Icon: a.Icon}
}

// EstablishAppearance names an unnamed definition for its scope: a Work Agent
// after the human it represents, a project Agent by its supplied initial name.
// Locating a definition again keeps what its human chose.
func (a *AgentDefinition) EstablishAppearance(employeeDisplayName, projectInitialName string) (bool, error) {
	if a.AgentName != "" {
		return false, nil
	}
	name := strings.TrimSpace(projectInitialName)
	if a.RepresentsHuman() {
		name = EmployeeDefaultAgentName(employeeDisplayName)
	}
	if name == "" {
		return false, ErrInvalidAgentDefinition
	}
	a.AgentName = name
	return true, nil
}

// ChangeAppearance admits only an Owner of a person's Work Agent (Appearance
// change) and, for an Application's Agent, its Application Owner, never the
// Application itself (Application appearance change).
func (a *AgentDefinition) ChangeAppearance(requester DefinitionRequester, name string, icon AgentIcon) (bool, error) {
	if !(a.RepresentsHuman() && a.IsOwner(requester.ID)) && !a.admitsApplicationOwner(requester) {
		return false, ErrAgentAppearanceForbidden
	}
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > maxAgentNameRunes {
		return false, ErrInvalidAgentAppearance
	}
	if err := icon.Validate(); err != nil {
		return false, err
	}
	if a.AgentName == name && a.Icon == icon {
		return false, nil
	}
	a.AgentName, a.Icon = name, icon
	return true, nil
}
