package infrastructure

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"

	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/pkg/pluginconfig"
	"gopkg.in/yaml.v3"
)

const maxMetadataFile = 2 << 20

type pluginManifest struct {
	Name        string                                      `json:"name"`
	Version     string                                      `json:"version"`
	Description string                                      `json:"description"`
	Skills      json.RawMessage                             `json:"skills"`
	Commands    json.RawMessage                             `json:"commands"`
	Agents      json.RawMessage                             `json:"agents"`
	MCPServers  json.RawMessage                             `json:"mcpServers"`
	UserConfig  map[string]application.ParameterDeclaration `json:"userConfig"`
	Strict      *bool                                       `json:"strict"`
}

// ParsePluginMetadata reads the acquired package without executing it.
// os.Root prevents a catalog component path or symlink from reading outside
// that package. This parser never downloads, extracts or executes plugin code.
func ParsePluginMetadata(directory, source, retrievalRef string, entry []byte) (application.PluginRelease, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return application.PluginRelease{}, err
	}
	defer root.Close()
	var declared pluginManifest
	if err := json.Unmarshal(entry, &declared); err != nil {
		return application.PluginRelease{}, errors.New("marketplace: invalid catalog entry")
	}
	manifest := pluginManifest{}
	data, err := readMetadata(root.FS(), ".claude-plugin/plugin.json")
	if err == nil {
		if err := json.Unmarshal(data, &manifest); err != nil {
			return application.PluginRelease{}, errors.New("marketplace: invalid plugin manifest")
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return application.PluginRelease{}, err
	}
	packageName, packageVersion := manifest.Name, manifest.Version
	if declared.Strict != nil && !*declared.Strict {
		manifest = declared
	}
	if packageName != "" {
		manifest.Name = packageName
	}
	if manifest.Name == "" {
		manifest.Name = declared.Name
	}
	if packageVersion != "" {
		manifest.Version = packageVersion
	}
	if manifest.Version == "" {
		manifest.Version = declared.Version
	}
	if manifest.Description == "" {
		manifest.Description = declared.Description
	}
	if !pluginconfig.ValidPluginName(manifest.Name) || manifest.Version == "" {
		return application.PluginRelease{}, errors.New("marketplace: valid package name and explicit version are required")
	}
	if manifest.Name != declared.Name || (declared.Version != "" && manifest.Version != declared.Version) {
		var native map[string]json.RawMessage
		if err := json.Unmarshal(entry, &native); err != nil {
			return application.PluginRelease{}, err
		}
		native["name"], _ = json.Marshal(manifest.Name)
		native["version"], _ = json.Marshal(manifest.Version)
		entry, err = json.Marshal(native)
		if err != nil {
			return application.PluginRelease{}, err
		}
	}
	result := application.PluginRelease{Slug: manifest.Name, Source: source, Version: manifest.Version, Description: manifest.Description, RetrievalRef: retrievalRef, NativeEntry: slices.Clone(entry), Parameters: manifest.UserConfig}
	if err := application.AssembleParameterDeclarationsDTO(result.Parameters).Validate(); err != nil {
		return application.PluginRelease{}, err
	}
	skillPaths, err := componentPaths(manifest.Skills, declared.Skills, []string{"skills"}, true)
	if err != nil {
		return application.PluginRelease{}, err
	}
	if len(manifest.Skills) == 0 && len(declared.Skills) == 0 {
		if _, err := fs.Stat(root.FS(), "skills"); errors.Is(err, fs.ErrNotExist) {
			skillPaths = append(skillPaths, ".")
		}
	}
	commandPaths, commands, err := commandMetadata(root.FS(), manifest.Commands, declared.Commands)
	if err != nil {
		return application.PluginRelease{}, err
	}
	agentPaths, err := componentPaths(manifest.Agents, declared.Agents, []string{"agents"}, false)
	if err != nil {
		return application.PluginRelease{}, err
	}
	seenSkills, seenAgents := map[string]bool{}, map[string]bool{}
	for _, group := range []struct {
		paths []string
		kind  string
	}{{skillPaths, "skill"}, {commandPaths, "command"}, {agentPaths, "agent"}} {
		for _, directory := range group.paths {
			files, err := componentFiles(root.FS(), directory, group.kind == "skill")
			if err != nil {
				return application.PluginRelease{}, err
			}
			for _, filename := range files {
				content, err := readMetadata(root.FS(), filename)
				if err != nil {
					return application.PluginRelease{}, err
				}
				meta := frontmatter(content)
				name := meta.Name
				if name == "" {
					if group.kind == "skill" {
						name = path.Base(path.Dir(filename))
					} else {
						name = strings.TrimSuffix(path.Base(filename), ".md")
					}
				}
				if name == "." {
					name = manifest.Name
				}
				if group.kind == "agent" {
					// Default agents/ preserves subfolder namespaces. Explicit
					// manifest file declarations name only the file/frontmatter.
					if len(manifest.Agents) == 0 && len(declared.Agents) == 0 {
						parent := strings.TrimPrefix(path.Dir(filename), "agents")
						parent = strings.Trim(parent, "/")
						if parent != "" {
							name = strings.ReplaceAll(parent, "/", ":") + ":" + name
						}
					}
					name = manifest.Name + ":" + name
					if seenAgents[name] {
						continue
					}
					seenAgents[name] = true
					result.Agents = append(result.Agents, application.AgentComponent{Name: name, Description: meta.Description})
				} else {
					name = manifest.Name + ":" + name
					if seenSkills[name] {
						continue
					}
					seenSkills[name] = true
					result.Skills = append(result.Skills, application.SkillComponent{Name: name, Description: meta.Description, ArgumentHint: meta.ArgumentHint})
				}
			}
		}
	}
	for _, command := range commands {
		command.Name = manifest.Name + ":" + command.Name
		if !seenSkills[command.Name] {
			seenSkills[command.Name] = true
			result.Skills = append(result.Skills, command)
		}
	}
	result.MCPServers, err = parseMCPServers(root.FS(), manifest.MCPServers, declared.MCPServers)
	if err != nil {
		return application.PluginRelease{}, err
	}
	return result, nil
}

func readMetadata(files fs.FS, name string) ([]byte, error) {
	file, err := files.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	value, err := io.ReadAll(io.LimitReader(file, maxMetadataFile+1))
	if err != nil {
		return nil, err
	}
	if len(value) > maxMetadataFile {
		return nil, errors.New("marketplace: component metadata exceeds size limit")
	}
	return value, nil
}

func componentPaths(manifest, entry json.RawMessage, defaults []string, additive bool) ([]string, error) {
	result := []string{}
	if additive || (len(manifest) == 0 && len(entry) == 0) {
		result = append(result, defaults...)
	}
	for _, raw := range []json.RawMessage{manifest, entry} {
		if len(raw) == 0 || string(raw) == "null" {
			continue
		}
		var paths []string
		var single string
		if json.Unmarshal(raw, &single) == nil {
			paths = []string{single}
		} else if err := json.Unmarshal(raw, &paths); err != nil {
			return nil, errors.New("marketplace: invalid component path declaration")
		}
		for _, name := range paths {
			name = strings.TrimRight(strings.TrimPrefix(name, "./"), "/")
			if name == "" {
				name = "."
			}
			if !fs.ValidPath(name) || strings.Contains(name, "\\") {
				return nil, errors.New("marketplace: component path escapes plugin")
			}
			if !slices.Contains(result, name) {
				result = append(result, name)
			}
		}
	}
	return result, nil
}

func componentFiles(files fs.FS, name string, skill bool) ([]string, error) {
	info, err := fs.Stat(files, name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return []string{name}, nil
	}
	if skill {
		if _, err := fs.Stat(files, path.Join(name, "SKILL.md")); err == nil {
			return []string{path.Join(name, "SKILL.md")}, nil
		}
		if name == "." {
			return nil, nil
		}
	}
	result := []string{}
	err = fs.WalkDir(files, name, func(filename string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if (skill && entry.Name() == "SKILL.md") || (!skill && strings.HasSuffix(entry.Name(), ".md")) {
			result = append(result, filename)
		}
		if len(result) > 1000 {
			return errors.New("marketplace: too many plugin components")
		}
		return nil
	})
	return result, err
}

type markdownMetadata struct {
	Name         string `yaml:"name"`
	Description  string `yaml:"description"`
	ArgumentHint string `yaml:"argument-hint"`
}

func frontmatter(content []byte) markdownMetadata {
	text := strings.ReplaceAll(string(content), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return markdownMetadata{}
	}
	end := strings.Index(text[4:], "\n---")
	if end < 0 {
		return markdownMetadata{}
	}
	var result markdownMetadata
	if yaml.Unmarshal([]byte(text[4:4+end]), &result) != nil {
		return markdownMetadata{}
	}
	return result
}
