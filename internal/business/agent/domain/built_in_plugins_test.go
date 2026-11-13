package domain_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// A Work requires the platform scope's Built-in Plugins and those its Work
// Caller's kind declares, each once; a kind without a scope adds nothing.
func TestBuiltInPluginsFollowTheWorkCallersKind(t *testing.T) {
	builtIns, err := domain.NewBuiltInPlugins([]string{"talgent"}, map[string][]string{"project": {"extension", "talgent"}, domain.CallerKindPerson: {"personal"}})
	require.NoError(t, err)
	require.Equal(t, []string{"talgent", "extension"}, builtIns.Required(domain.WorkCaller{Kind: "project", ID: "p"}))
	require.Equal(t, []string{"talgent", "personal"}, builtIns.Required(domain.WorkCaller{Kind: domain.CallerKindPerson, ID: "u"}))
	require.Equal(t, []string{"talgent"}, builtIns.Required(domain.WorkCaller{Kind: domain.CallerKindApplication, ID: "a"}))
	require.Equal(t, []string{"talgent", "extension", "definition", "caller"}, builtIns.ComposeRequiredPlugins(domain.WorkCaller{Kind: "project", ID: "p"}, []string{"definition"}, []string{"caller"}))
}

// Each scope names valid slugs once, under a valid Work Caller kind.
func TestBuiltInPluginsRefuseAnInvalidScope(t *testing.T) {
	for name, kinds := range map[string]map[string][]string{
		"kind with a colon": {"pro:ject": {"extension"}},
		"upper-case kind":   {"Project": {"extension"}},
		"repeated slug":     {"project": {"extension", "extension"}},
		"invalid slug":      {"project": {"Not A Slug"}},
	} {
		_, err := domain.NewBuiltInPlugins(nil, kinds)
		require.Error(t, err, name)
	}
}

// Readers read every file of a Work's own File Spaces, and in its File
// Home's only the Artifacts the Work published.
func TestSharedSpaceReading(t *testing.T) {
	require.True(t, domain.ReadableWorkFile("w", false, false, ""), "an input in the Work's own space")
	require.True(t, domain.ReadableWorkFile("w", true, true, "w"), "its own Artifact in its File Home")
	require.False(t, domain.ReadableWorkFile("w", true, true, "other"), "another Work's Artifact in the File Home")
	require.False(t, domain.ReadableWorkFile("w", true, false, "w"), "an input of the File Home")
}
