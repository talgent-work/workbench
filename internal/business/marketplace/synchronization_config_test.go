package marketplace

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/go-jimu/components/config/loader"
	"github.com/stretchr/testify/require"
)

// A deployment's configuration profile adds a catalog source beside the
// defaults' sources, without listing them again.
func TestAProfileAddsACatalogSourceBesideTheDefaults(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	require.True(t, ok)
	defaults, err := os.ReadFile(filepath.Join(filepath.Dir(source), "../../../configs/controlplane/defaults.yml"))
	require.NoError(t, err)
	directory := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(directory, "defaults.yml"), defaults, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(directory, "controlplane_extension.yml"), []byte(`marketplace:
  synchronization:
    acquisition:
      sources:
        extension:
          uri: https://example.invalid/extension-marketplace.git
          priority: 50
`), 0o600))
	t.Setenv("JIMU_PROFILES_ACTIVE", "extension")

	var config struct {
		Marketplace struct {
			Synchronization SynchronizationConfig `json:"synchronization"`
		} `json:"marketplace"`
	}
	require.NoError(t, loader.Load(&config, loader.WithConfigurationDirectory(directory, "defaults"), loader.WithConfigFilePrefix("controlplane")))
	sources := config.Marketplace.Synchronization.Acquisition.Sources
	names := make([]string, 0, len(sources))
	for name := range sources {
		names = append(names, name)
	}
	slices.Sort(names)
	require.Equal(t, []string{"claude-plugins-official", "extension", "talgent"}, names)
	require.Equal(t, 50, sources["extension"].Priority)
	require.True(t, sources["talgent"].Owned)
}
