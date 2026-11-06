package nativeprocess_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/nativeprocess"
)

func TestPluginLoadsDistinguishesInventoryFromMissingOrMalformedEvidence(t *testing.T) {
	for _, input := range []string{`{"type":"system","subtype":"init"}`, `{"type":"system","subtype":"init","plugins":null}`, `{"type":"system","subtype":"status","plugins":[]}`, `{"type":"system","subtype":"init","plugins":{}}`, `{"type":"system","subtype":"init","plugins":[{}]}`, `{"type":"system","subtype":"init","plugins":[{"name":"x"},{"name":"x"}]}`} {
		var message nativeprocess.Message
		require.NoError(t, json.Unmarshal([]byte(input), &message))
		_, observed := nativeprocess.PluginLoads(&message)
		require.False(t, observed, input)
	}
	var message nativeprocess.Message
	require.NoError(t, json.Unmarshal([]byte(`{"type":"system","subtype":"init","plugins":[{"name":"runtime","version":"1.2.3","path":"/private/plugin/path","source":"runtime@work"}]}`), &message))
	plugins, observed := nativeprocess.PluginLoads(&message)
	require.True(t, observed)
	require.Equal(t, []command.LoadedPlugin{{Slug: "runtime", Version: "1.2.3"}}, plugins)
	require.NoError(t, json.Unmarshal([]byte(`{"type":"system","subtype":"init","plugins":[]}`), &message))
	plugins, observed = nativeprocess.PluginLoads(&message)
	require.True(t, observed)
	require.Empty(t, plugins)
}
