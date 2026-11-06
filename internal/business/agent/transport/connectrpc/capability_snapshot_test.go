package connectrpc

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/pkg/pluginconfig"
)

func TestWorkSnapshotQueryRedactsSensitiveValuesWithoutChangingExecutionSnapshot(t *testing.T) {
	snapshot := domain.CapabilitySnapshot{Plugins: []domain.SelectedPlugin{{Release: domain.PluginRelease{Slug: "tools", Source: "owned", Version: "1", RetrievalRef: "archive", NativeEntry: []byte(`{"name":"tools","userConfig":{"secret":{"sensitive":true,"default":"hidden-default"}}}`), Parameters: pluginconfig.ParameterDeclarations{"secret": {Type: "string", Title: "Secret", Description: "Sensitive option", Sensitive: true, Default: "hidden-default"}, "visible": {Type: "string", Title: "Visible", Description: "Ordinary option"}}}, Configuration: pluginconfig.Configuration{"secret": "hidden-manual", "visible": "shown"}}}}
	output, err := capabilitySnapshotToProto(snapshot)
	require.NoError(t, err)
	require.NotContains(t, output.String(), "hidden-manual")
	require.NotContains(t, output.String(), "hidden-default")
	require.Equal(t, "shown", output.Plugins[0].Configuration.AsMap()["visible"])
	require.True(t, output.Plugins[0].Release.Parameters["secret"].HasDefault)
	require.Equal(t, "hidden-manual", snapshot.Plugins[0].Configuration["secret"], "execution retains authorized Sensitive configuration")
	require.Equal(t, "hidden-default", snapshot.Plugins[0].Release.Parameters["secret"].Default)
}
