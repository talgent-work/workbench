package domain

import "reflect"

// ProviderAccessBinding contains verified service requirements, never a personal
// authorization reference. Identity is selected by the consuming Work.
type ProviderAccessBinding struct{ Provider, Target, Authentication string }

// ProviderCompatibility is supplied by Marketplace's protocol adapter. The
// aggregate accepts only a binding for the exact configuration it verified.
type ProviderCompatibility struct {
	Configuration MCPConfiguration
	Binding       ProviderAccessBinding
}

func validateProviderConfiguration(configuration MCPConfiguration, binding ProviderAccessBinding, evidence ProviderCompatibility) error {
	if !reflect.DeepEqual(configuration, evidence.Configuration) || binding != evidence.Binding || binding.Provider == "" || binding.Target == "" || binding.Authentication != "credential_proxy" {
		return ErrInvalidConnector
	}
	return configuration.Validate()
}
