package infrastructure

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"maps"
	"slices"
	"strings"

	"github.com/talgent-work/workbench/internal/business/marketplace/application"
)

// Command maps replace the default commands/ scan just like explicit paths.
// Their keys name commands even when a source file has a different basename
// or frontmatter name. NativeEntry retains the original execution declaration.
func commandMetadata(files fs.FS, manifest, entry json.RawMessage) ([]string, []application.SkillComponent, error) {
	declarations := []json.RawMessage{manifest, entry}
	var components []application.SkillComponent
	for i, raw := range declarations {
		if !bytes.HasPrefix(bytes.TrimSpace(raw), []byte("{")) {
			continue
		}
		var commands map[string]struct {
			Source       *string `json:"source"`
			Content      *string `json:"content"`
			Description  *string `json:"description"`
			ArgumentHint *string `json:"argumentHint"`
		}
		if err := json.Unmarshal(raw, &commands); err != nil {
			return nil, nil, errors.New("marketplace: invalid command declaration")
		}
		if len(commands) > 1000 {
			return nil, nil, errors.New("marketplace: too many plugin commands")
		}
		// An explicit empty path list keeps componentPaths from restoring defaults.
		declarations[i] = json.RawMessage(`[]`)
		for _, name := range slices.Sorted(maps.Keys(commands)) {
			command := commands[name]
			if strings.TrimSpace(name) == "" || (command.Source == nil) == (command.Content == nil) {
				return nil, nil, errors.New("marketplace: command requires exactly one source or content")
			}
			var content []byte
			if command.Source != nil {
				filename := strings.TrimPrefix(*command.Source, "./")
				if !fs.ValidPath(filename) || strings.Contains(filename, "\\") {
					return nil, nil, errors.New("marketplace: command path escapes plugin")
				}
				var err error
				content, err = readMetadata(files, filename)
				if err != nil {
					return nil, nil, err
				}
			} else {
				content = []byte(*command.Content)
				if len(content) > maxMetadataFile {
					return nil, nil, errors.New("marketplace: component metadata exceeds size limit")
				}
			}
			metadata := frontmatter(content)
			if command.Description != nil {
				metadata.Description = *command.Description
			}
			if command.ArgumentHint != nil {
				metadata.ArgumentHint = *command.ArgumentHint
			}
			components = append(components, application.SkillComponent{Name: name, Description: metadata.Description, ArgumentHint: metadata.ArgumentHint})
		}
	}
	paths, err := componentPaths(declarations[0], declarations[1], []string{"commands"}, false)
	return paths, components, err
}
