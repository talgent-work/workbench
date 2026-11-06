package infrastructure_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/marketplace/infrastructure"
)

func writeMetadata(t *testing.T, root, name, content string) {
	t.Helper()
	filename := filepath.Join(root, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(filename), 0o700))
	require.NoError(t, os.WriteFile(filename, []byte(content), 0o600))
}

func TestPluginMetadataMatchesVersionedNativeComponents(t *testing.T) {
	root := t.TempDir()
	writeMetadata(t, root, ".claude-plugin/plugin.json", `{"name":"reviewer","version":"2.0.0","userConfig":{"enabled":{"type":"boolean","title":"Feature","description":"Switch","default":true},"paths":{"type":"string","title":"Paths","description":"Included paths","multiple":true,"default":["src"]}}}`)
	writeMetadata(t, root, "skills/audit/SKILL.md", "---\nname: inspect\ndescription: Inspect a change\nargument-hint: '[file]'\n---\nRead the file.\n")
	writeMetadata(t, root, "agents/review/security.md", "---\nname: audit\ndescription: Security review\n---\nReview security.\n")
	writeMetadata(t, root, ".mcp.json", `{"mcpServers":{"docs":{"type":"http","url":"https://example.com/mcp"}}}`)
	plugin, err := infrastructure.ParsePluginMetadata(root, "upstream", "git:fixed", []byte(`{"name":"reviewer","version":"2.0.0","source":{"source":"github","repo":"org/reviewer","sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}`))
	require.NoError(t, err)
	require.Equal(t, "2.0.0", plugin.Version)
	require.Len(t, plugin.Skills, 1)
	require.Equal(t, "reviewer:inspect", plugin.Skills[0].Name)
	require.Equal(t, "[file]", plugin.Skills[0].ArgumentHint)
	require.Equal(t, "reviewer:review:audit", plugin.Agents[0].Name)
	require.Equal(t, "docs", plugin.MCPServers[0].Name)
	var mcp map[string]any
	require.NoError(t, json.Unmarshal(plugin.MCPServers[0].Definition, &mcp))
	require.Equal(t, "https://example.com/mcp", mcp["url"])
	require.Equal(t, true, plugin.Parameters["enabled"].Default)
}

func TestMetadataUsesPackageVersionAndRejectsEscapingComponent(t *testing.T) {
	root := t.TempDir()
	writeMetadata(t, root, ".claude-plugin/plugin.json", `{"name":"reviewer","version":"2.0.0"}`)
	plugin, err := infrastructure.ParsePluginMetadata(root, "upstream", "fixed", []byte(`{"name":"reviewer","version":"1.0.0"}`))
	require.NoError(t, err)
	require.Equal(t, "2.0.0", plugin.Version)
	var native map[string]any
	require.NoError(t, json.Unmarshal(plugin.NativeEntry, &native))
	require.Equal(t, "2.0.0", native["version"])
	writeMetadata(t, root, ".claude-plugin/plugin.json", `{"name":"reviewer","version":"2.0.0","agents":"../outside.md"}`)
	_, err = infrastructure.ParsePluginMetadata(root, "upstream", "fixed", []byte(`{"name":"reviewer"}`))
	require.ErrorContains(t, err, "escapes")
	outside := t.TempDir()
	writeMetadata(t, outside, "private.md", "This must not enter plugin metadata")
	require.NoError(t, os.Symlink(filepath.Join(outside, "private.md"), filepath.Join(root, "outside.md")))
	writeMetadata(t, root, ".claude-plugin/plugin.json", `{"name":"reviewer","version":"2.0.0","agents":"./outside.md"}`)
	_, err = infrastructure.ParsePluginMetadata(root, "upstream", "fixed", []byte(`{"name":"reviewer"}`))
	require.Error(t, err)
}

func TestExplicitAgentPathsAndSingleSkillPlugin(t *testing.T) {
	root := t.TempDir()
	writeMetadata(t, root, ".claude-plugin/plugin.json", `{"name":"reviewer","version":"2.0.0","agents":"./custom/security.md"}`)
	writeMetadata(t, root, "custom/security.md", "---\nname: audit\ndescription: Review\n---\nReview\n")
	writeMetadata(t, root, "SKILL.md", "---\nname: inspect\ndescription: Inspect\n---\nInspect\n")
	plugin, err := infrastructure.ParsePluginMetadata(root, "upstream", "fixed", []byte(`{"name":"reviewer"}`))
	require.NoError(t, err)
	require.Equal(t, "reviewer:audit", plugin.Agents[0].Name)
	require.Equal(t, "reviewer:inspect", plugin.Skills[0].Name)
}

func TestCommandMetadataRejectsAmbiguousAndEscapingSources(t *testing.T) {
	for _, declaration := range []string{
		`{"source":"./command.md","content":"inline"}`,
		`{"description":"no body"}`,
		`{"source":"../outside.md"}`,
		`{"source":"./linked.md"}`,
	} {
		t.Run(declaration, func(t *testing.T) {
			root := t.TempDir()
			outside := t.TempDir()
			writeMetadata(t, outside, "private.md", "private content")
			require.NoError(t, os.Symlink(filepath.Join(outside, "private.md"), filepath.Join(root, "linked.md")))
			writeMetadata(t, root, "command.md", "file content")
			writeMetadata(t, root, ".claude-plugin/plugin.json", `{"name":"reviewer","version":"2.0.0","commands":{"check":`+declaration+`}}`)
			_, err := infrastructure.ParsePluginMetadata(root, "upstream", "fixed", []byte(`{"name":"reviewer"}`))
			require.Error(t, err)
			require.NotContains(t, err.Error(), "private content")
		})
	}
}

func TestMetadataAcceptsDirectorySuffixAndPluginsWithoutSkills(t *testing.T) {
	root := t.TempDir()
	writeMetadata(t, root, ".claude-plugin/plugin.json", `{"name":"reviewer","version":"1.0.0","skills":"./skills/"}`)
	writeMetadata(t, root, "skills/check/SKILL.md", "Review changes")
	plugin, err := infrastructure.ParsePluginMetadata(root, "upstream", "fixed", []byte(`{"name":"reviewer"}`))
	require.NoError(t, err)
	require.Len(t, plugin.Skills, 1)
	require.NoError(t, os.RemoveAll(filepath.Join(root, "skills")))
	writeMetadata(t, root, ".mcp.json", `{"mcpServers":{"docs":{"type":"http","url":"https://example.com/mcp"}}}`)
	plugin, err = infrastructure.ParsePluginMetadata(root, "upstream", "fixed", []byte(`{"name":"reviewer"}`))
	require.NoError(t, err)
	require.Empty(t, plugin.Skills)
	require.Len(t, plugin.MCPServers, 1)
}

func TestMetadataUsesPackageNameForIdentityAndNativeNamespaces(t *testing.T) {
	root := t.TempDir()
	writeMetadata(t, root, ".claude-plugin/plugin.json", `{"name":"Actual-Plugin","version":"2.0.0"}`)
	writeMetadata(t, root, "agents/reviewer.md", "Review")
	writeMetadata(t, root, "skills/check/SKILL.md", "Check")
	for _, strict := range []string{"true", "false"} {
		t.Run(strict, func(t *testing.T) {
			plugin, err := infrastructure.ParsePluginMetadata(root, "upstream", "fixed", []byte(`{"name":"catalog-alias","version":"1.0.0","strict":`+strict+`}`))
			require.NoError(t, err)
			require.Equal(t, "Actual-Plugin", plugin.Slug)
			require.Equal(t, "2.0.0", plugin.Version)
			require.Equal(t, "Actual-Plugin:reviewer", plugin.Agents[0].Name)
			require.Equal(t, "Actual-Plugin:check", plugin.Skills[0].Name)
			var entry map[string]any
			require.NoError(t, json.Unmarshal(plugin.NativeEntry, &entry))
			require.Equal(t, "Actual-Plugin", entry["name"])
			require.Equal(t, "2.0.0", entry["version"])
		})
	}
}
