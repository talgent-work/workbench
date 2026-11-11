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

func loadSynchronization(t *testing.T, profile string) SynchronizationConfig {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	require.True(t, ok)
	defaults, err := os.ReadFile(filepath.Join(filepath.Dir(source), "../../../configs/controlplane/defaults.yml"))
	require.NoError(t, err)
	directory := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(directory, "defaults.yml"), defaults, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(directory, "controlplane_extension.yml"), []byte(profile), 0o600))
	t.Setenv("JIMU_PROFILES_ACTIVE", "extension")

	var config struct {
		Marketplace struct {
			Synchronization SynchronizationConfig `json:"synchronization"`
		} `json:"marketplace"`
	}
	require.NoError(t, loader.Load(&config, loader.WithConfigurationDirectory(directory, "defaults"), loader.WithConfigFilePrefix("controlplane")))
	return config.Marketplace.Synchronization
}

// A deployment's configuration profile adds a catalog source beside the
// defaults' sources, without listing them again.
func TestAProfileAddsACatalogSourceBesideTheDefaults(t *testing.T) {
	sources := loadSynchronization(t, `marketplace:
  synchronization:
    acquisition:
      sources:
        extension:
          uri: https://example.invalid/extension-marketplace.git
          priority: 50
`).Acquisition.Sources
	names := make([]string, 0, len(sources))
	for name := range sources {
		names = append(names, name)
	}
	slices.Sort(names)
	require.Equal(t, []string{"claude-plugins-official", "extension", "talgent"}, names)
	require.Equal(t, 50, sources["extension"].Priority)
	require.True(t, sources["talgent"].Owned)
}

// Sources are read without credentials. A profile still naming a deploy key
// for an SSH source is refused at startup, not read without the key.
func TestAProfileWithACredentialedSourceIsRefused(t *testing.T) {
	config := loadSynchronization(t, `marketplace:
  synchronization:
    enabled: true
    acquisition:
      archive-base-url: https://workbench.example.invalid/marketplace/archives
      sources:
        extension:
          uri: git@github.com:example/extension-marketplace.git
          owned: true
          priority: 50
          credentials:
            ssh-key-file: /etc/talgent/profile/ssh-privatekey
            known-hosts-file: /etc/talgent/profile/known_hosts
`)
	err := config.Validate()
	require.ErrorContains(t, err, `catalog source "extension" must be a public HTTPS address`)
}
