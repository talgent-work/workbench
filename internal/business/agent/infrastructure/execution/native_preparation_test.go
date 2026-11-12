package execution

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// A retained Plugin release whose native entry does not match it can never be
// installed: the start's specification fails definitely, naming the release.
func TestNativePreparationNamesTheInvalidPluginRelease(t *testing.T) {
	for name, tc := range map[string]struct {
		entry string
		cause string
	}{
		"renamed":    {entry: `{"name":"talgent-runtime","version":"1"}`, cause: "Plugin release talgent 1 is invalid: its native entry is named talgent-runtime"},
		"versioned":  {entry: `{"name":"talgent","version":"2"}`, cause: "Plugin release talgent 1 is invalid: its native entry is version 2"},
		"unreadable": {entry: `not json`, cause: "Plugin release talgent 1 is invalid: its native entry is unreadable"},
	} {
		t.Run(name, func(t *testing.T) {
			input := request()
			input.Capabilities.Plugins = []domain.SelectedPlugin{{Release: domain.PluginRelease{CatalogName: "talgent", Slug: "talgent", Source: "fixture", Version: "1", RetrievalRef: "archive:talgent", NativeEntry: []byte(tc.entry)}}}
			_, err := NativeLaunchConfig{Binary: "claude"}.prepare(sandboxLayout, input, owner(input), "")
			cause, definite := domain.DefiniteStartCause(err)
			require.True(t, definite)
			require.Equal(t, tc.cause, cause)
		})
	}
}
