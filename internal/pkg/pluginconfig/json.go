package pluginconfig

// JSONValue preserves scalar types and turns JSON arrays of strings into the
// native multiple-string representation. Invalid shapes remain invalid; a bad
// list must never be collapsed into one of its scalar elements.
func JSONValue(value any) any {
	values, ok := value.([]any)
	if !ok {
		return value
	}
	result := make([]string, len(values))
	for i, entry := range values {
		text, ok := entry.(string)
		if !ok {
			return value
		}
		result[i] = text
	}
	return result
}

func FromJSON(values map[string]any) Configuration {
	result := make(Configuration, len(values))
	for name, value := range values {
		result[name] = JSONValue(value)
	}
	return result
}
