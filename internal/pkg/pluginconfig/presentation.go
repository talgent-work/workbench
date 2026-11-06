package pluginconfig

import "encoding/json"

// PublicNativeEntry removes sensitive declaration defaults from discovery and
// snapshot queries. The retained original remains the native installation input.
func PublicNativeEntry(entry []byte, sensitive bool) []byte {
	if !sensitive {
		return append([]byte(nil), entry...)
	}
	var value map[string]any
	if json.Unmarshal(entry, &value) != nil {
		return nil
	}
	for _, field := range []string{"userConfig", "user_config"} {
		declarations, ok := value[field].(map[string]any)
		if !ok {
			continue
		}
		for _, raw := range declarations {
			if declaration, ok := raw.(map[string]any); ok && declaration["sensitive"] == true {
				delete(declaration, "default")
			}
		}
	}
	result, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return result
}
