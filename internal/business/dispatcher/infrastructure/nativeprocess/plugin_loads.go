package nativeprocess

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
)

// PluginLoads maps a complete native inventory, not installation evidence.
// Missing or unrecognized payloads are not an empty inventory. Local paths and
// provider diagnostics never cross this boundary.
func PluginLoads(message *Message) ([]command.LoadedPlugin, bool) {
	if message == nil || message.Type != "system" || message.Subtype != "init" {
		return nil, false
	}
	raw := bytes.TrimSpace(message.Plugins)
	if len(raw) == 0 || raw[0] != '[' {
		return nil, false
	}
	var plugins []struct{ Name, Version string }
	if json.Unmarshal(raw, &plugins) != nil || len(plugins) > 4096 {
		return nil, false
	}
	result := make([]command.LoadedPlugin, 0, len(plugins))
	seen := make(map[string]bool, len(plugins))
	for _, plugin := range plugins {
		if strings.TrimSpace(plugin.Name) == "" || len(plugin.Name) > 128 || len(plugin.Version) > 256 || seen[plugin.Name] {
			return nil, false
		}
		seen[plugin.Name] = true
		result = append(result, command.LoadedPlugin{Slug: plugin.Name, Version: plugin.Version})
	}
	return result, true
}
