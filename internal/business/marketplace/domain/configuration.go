package domain

import (
	"github.com/go-playground/validator/v10"
	"github.com/talgent-work/workbench/internal/pkg/pluginconfig"
)

// Declarations share the native parameter grammar with Agent startup. Personal
// configuration remains owned by InstalledPlugin, independently of any Work.
type ParameterDeclaration = pluginconfig.ParameterDeclaration
type ParameterDeclarations = pluginconfig.ParameterDeclarations
type ConfigurationError = pluginconfig.ConfigurationError

var ErrInvalidConfiguration = pluginconfig.ErrInvalidConfiguration

var domainValidator = validator.New()

func invalidParameter(name, reason string) error {
	return &ConfigurationError{Parameter: name, Reason: reason}
}

type Configuration map[string]any

func (c Configuration) Clone() Configuration {
	return Configuration(pluginconfig.Configuration(c).Clone())
}

func (c Configuration) Effective(declarations ParameterDeclarations) (Configuration, error) {
	values, err := pluginconfig.Configuration(c).Effective(declarations)
	return Configuration(values), err
}
