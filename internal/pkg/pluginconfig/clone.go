package pluginconfig

import "slices"

// Clone isolates mutable containers inside otherwise immutable declarations.
func (d ParameterDeclarations) Clone() ParameterDeclarations {
	result := make(ParameterDeclarations, len(d))
	for name, declaration := range d {
		declaration.Options = slices.Clone(declaration.Options)
		if values, ok := declaration.Default.([]string); ok {
			declaration.Default = slices.Clone(values)
		}
		if declaration.Min != nil {
			value := *declaration.Min
			declaration.Min = &value
		}
		if declaration.Max != nil {
			value := *declaration.Max
			declaration.Max = &value
		}
		result[name] = declaration
	}
	return result
}
