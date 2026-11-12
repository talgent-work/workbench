package domain

import (
	"errors"
	"slices"
	"strings"
)

var (
	// ErrWorkDefaultsForbidden refuses reading or changing Work defaults to
	// anyone but an Application or its Application Owner, and for any
	// definition but an Application's.
	ErrWorkDefaultsForbidden = errors.New("agent: only an Application or its Application Owner changes its Agent's Work defaults")
	// ErrInvalidWorkDefaults refuses a default model that is not provided or
	// a default thinking level that model does not offer.
	ErrInvalidWorkDefaults = errors.New("agent: a default model is a provided Model Configuration and a default thinking level one it offers")
)

// WorkDefaults is what a definition's new Works start with when their start
// names none: the startup Agent, and, for an Application's definition only, a
// Model Configuration and a thinking level (Work defaults). A zero model
// leaves the platform's first provided one, and an empty thinking level the
// model's own default.
type WorkDefaults struct {
	StartupAgent string
	// ModelConfigurationID names a Model Configuration; never the machine's
	// own Claude login, which an Application's Cloud Works do not have.
	ModelConfigurationID string
	ThinkingLevel        string
}

// ModelOffer is the supplied fact about a default model: whether the platform
// provides it now and the thinking levels its Model Template offers.
type ModelOffer struct {
	Provided       bool
	ThinkingLevels []string
}

func (d WorkDefaults) normalized() WorkDefaults {
	return WorkDefaults{StartupAgent: strings.TrimSpace(d.StartupAgent), ModelConfigurationID: strings.TrimSpace(d.ModelConfigurationID), ThinkingLevel: strings.TrimSpace(d.ThinkingLevel)}
}

// holdsModel reports whether the defaults name a model or thinking level,
// which only an Application's definition holds.
func (d WorkDefaults) holdsModel() bool {
	return d.ModelConfigurationID != "" || d.ThinkingLevel != ""
}

func (d WorkDefaults) validate() error {
	if d.ModelConfigurationID != "" {
		if err := (WorkModelSelection{ConfigurationID: d.ModelConfigurationID}).Validate(); err != nil {
			return ErrInvalidWorkDefaults
		}
	}
	if d.ThinkingLevel != "" && !slices.Contains(ThinkingLevels, d.ThinkingLevel) {
		return ErrInvalidWorkDefaults
	}
	return nil
}

// Model is the default Model Configuration as a Work model selection; zero
// when the defaults name none.
func (d WorkDefaults) Model() WorkModelSelection {
	return WorkModelSelection{ConfigurationID: d.ModelConfigurationID}
}

// ReadWorkDefaults returns an Application's Agent's Work defaults to the
// Application or its Application Owner.
func (a *AgentDefinition) ReadWorkDefaults(requester DefinitionRequester) (WorkDefaults, error) {
	if !a.admitsWorkDefaults(requester) {
		return WorkDefaults{}, ErrWorkDefaultsForbidden
	}
	return a.WorkDefaults, nil
}

// ChangeWorkDefaults replaces an Application's Agent's Work defaults for the
// Works it creates afterwards, at the request of the Application or its
// Application Owner (Application Work defaults). A default model is one the
// platform provides, and a default thinking level is one the default model
// offers, or, with no default model, one the platform's first provided model
// offers, which offer describes (Work defaults validity). It reports whether
// the defaults changed.
func (a *AgentDefinition) ChangeWorkDefaults(requester DefinitionRequester, defaults WorkDefaults, offer ModelOffer) (bool, error) {
	if !a.admitsWorkDefaults(requester) {
		return false, ErrWorkDefaultsForbidden
	}
	defaults = defaults.normalized()
	if err := defaults.validate(); err != nil {
		return false, err
	}
	if defaults.ModelConfigurationID != "" && !offer.Provided {
		return false, ErrInvalidWorkDefaults
	}
	if defaults.ThinkingLevel != "" && !slices.Contains(offer.ThinkingLevels, defaults.ThinkingLevel) {
		return false, ErrInvalidWorkDefaults
	}
	if defaults == a.WorkDefaults {
		return false, nil
	}
	a.WorkDefaults = defaults
	return true, nil
}

// admitsWorkDefaults admits the Application, the one Owner of its Agent, and
// its Application Owner, for an Application's definition only.
func (a *AgentDefinition) admitsWorkDefaults(requester DefinitionRequester) bool {
	return a.OwningApplication() != "" && a.admitsConfiguration(requester)
}
