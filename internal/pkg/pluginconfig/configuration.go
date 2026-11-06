// Package pluginconfig implements the native parameter declaration and typed
// value contract shared by personal installation and Work startup validation.
// It owns no installation, catalog, user identity, or Work lifecycle.
package pluginconfig

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/go-playground/validator/v10"
)

var (
	ErrInvalidConfiguration = errors.New("invalid plugin configuration")
	declarationValidator    = validator.New()
	parameterName           = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
)

// Configuration retains typed, explicitly supplied values. Absence is distinct
// from an explicit false, zero, empty string, or value equal to a default.
type Configuration map[string]any

type ParameterDeclaration struct {
	Type        string `validate:"oneof=string number boolean directory file"`
	Title       string `validate:"required"`
	Description string `validate:"required"`
	Required    bool
	Sensitive   bool
	Multiple    bool
	Default     any
	Options     []string
	Min         *float64
	Max         *float64
}

type ParameterDeclarations map[string]ParameterDeclaration

// ConfigurationError identifies a field and reason without including its value.
type ConfigurationError struct {
	Parameter string
	Reason    string
}

func (e *ConfigurationError) Error() string {
	return fmt.Sprintf("%s: %s: %s", ErrInvalidConfiguration, e.Parameter, e.Reason)
}

func (e *ConfigurationError) Unwrap() error { return ErrInvalidConfiguration }

func invalidParameter(name, reason string) error {
	return &ConfigurationError{Parameter: name, Reason: reason}
}

func (d ParameterDeclarations) Validate() error {
	for name, declaration := range d {
		if !parameterName.MatchString(name) {
			return invalidParameter(name, "invalid parameter name")
		}
		if err := declaration.Validate(); err != nil {
			return invalidParameter(name, "invalid parameter declaration")
		}
	}
	return nil
}

func (d ParameterDeclaration) Validate() error {
	if err := declarationValidator.Struct(d); err != nil {
		return ErrInvalidConfiguration
	}
	if d.Multiple && d.Type != "string" {
		return ErrInvalidConfiguration
	}
	if (d.Min != nil || d.Max != nil) && d.Type != "number" {
		return ErrInvalidConfiguration
	}
	for _, bound := range []*float64{d.Min, d.Max} {
		if bound != nil && (math.IsNaN(*bound) || math.IsInf(*bound, 0)) {
			return ErrInvalidConfiguration
		}
	}
	if d.Min != nil && d.Max != nil && *d.Min > *d.Max {
		return ErrInvalidConfiguration
	}
	if d.Options != nil {
		if len(d.Options) == 0 || d.Type != "string" || d.Multiple || d.Sensitive || (d.Default == nil && !d.Required) {
			return ErrInvalidConfiguration
		}
		seen := make(map[string]bool, len(d.Options))
		for _, option := range d.Options {
			key := strings.ToLower(option)
			if option == "" || len([]rune(option)) > 64 || strings.TrimSpace(option) != option || seen[key] {
				return ErrInvalidConfiguration
			}
			for _, char := range option {
				if unicode.IsControl(char) || unicode.Is(unicode.Cf, char) || (unicode.IsSpace(char) && char != ' ') {
					return ErrInvalidConfiguration
				}
			}
			seen[key] = true
		}
	}
	if d.Default != nil {
		return d.ValidateValue(d.Default)
	}
	return nil
}

func (d ParameterDeclaration) ValidateValue(value any) error {
	if d.Multiple {
		values, ok := value.([]string)
		if !ok || (d.Required && len(values) == 0) {
			return ErrInvalidConfiguration
		}
		for _, entry := range values {
			if d.Required && strings.TrimSpace(entry) == "" {
				return ErrInvalidConfiguration
			}
		}
		return nil
	}
	switch d.Type {
	case "string", "directory", "file":
		text, ok := value.(string)
		if !ok || (d.Required && strings.TrimSpace(text) == "") || (len(d.Options) > 0 && !slices.Contains(d.Options, text)) {
			return ErrInvalidConfiguration
		}
	case "number":
		number, ok := value.(float64)
		if !ok || math.IsNaN(number) || math.IsInf(number, 0) || (d.Min != nil && number < *d.Min) || (d.Max != nil && number > *d.Max) {
			return ErrInvalidConfiguration
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return ErrInvalidConfiguration
		}
	default:
		return ErrInvalidConfiguration
	}
	return nil
}

func (c Configuration) Clone() Configuration {
	result := make(Configuration, len(c))
	for name, value := range c {
		if values, ok := value.([]string); ok {
			value = slices.Clone(values)
		}
		result[name] = value
	}
	return result
}

// Effective interprets retained manual values against this release. Removed
// declarations are not passed to the runtime, and never rewrite personal data.
func (c Configuration) Effective(declarations ParameterDeclarations) (Configuration, error) {
	if err := declarations.Validate(); err != nil {
		return nil, err
	}
	result := make(Configuration)
	for name, declaration := range declarations {
		value, present := c[name]
		if !present {
			value = declaration.Default
			if value == nil {
				if declaration.Required {
					return nil, invalidParameter(name, "required value is missing")
				}
				continue
			}
		}
		if err := declaration.ValidateValue(value); err != nil {
			return nil, invalidParameter(name, "value does not satisfy the declaration")
		}
		result[name] = value
	}
	return result.Clone(), nil
}
