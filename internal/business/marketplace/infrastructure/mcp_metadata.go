package infrastructure

import (
	"encoding/json"
	"errors"
	"io/fs"
	"slices"
	"strings"

	"github.com/talgent-work/workbench/internal/business/marketplace/application"
)

func parseMCPServers(files fs.FS, declarations ...json.RawMessage) ([]application.MCPComponent, error) {
	servers := map[string]json.RawMessage{}
	merge := func(data []byte) error {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(data, &object); err != nil {
			return errors.New("marketplace: invalid MCP declaration")
		}
		if nested, ok := object["mcpServers"]; ok {
			object = nil // Decoding into a reused map otherwise leaves the wrapper key.
			if err := json.Unmarshal(nested, &object); err != nil {
				return errors.New("marketplace: invalid MCP server map")
			}
		}
		for name, value := range object {
			servers[name] = slices.Clone(value)
		}
		return nil
	}
	defaults, err := readMetadata(files, ".mcp.json")
	if err == nil {
		if err := merge(defaults); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	for _, declaration := range declarations {
		if len(declaration) == 0 || string(declaration) == "null" {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(string(declaration)), "{") {
			if err := merge(declaration); err != nil {
				return nil, err
			}
			continue
		}
		paths, err := componentPaths(declaration, nil, nil, false)
		if err != nil {
			return nil, err
		}
		for _, filename := range paths {
			content, err := readMetadata(files, filename)
			if err != nil {
				return nil, err
			}
			if err := merge(content); err != nil {
				return nil, err
			}
		}
	}
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	slices.Sort(names)
	result := make([]application.MCPComponent, 0, len(names))
	for _, name := range names {
		result = append(result, application.MCPComponent{Name: name, Definition: servers[name]})
	}
	return result, nil
}
