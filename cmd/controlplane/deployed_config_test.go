package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/go-jimu/components/config/loader"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// The configuration the Kubernetes base mounts passes the same validation the
// controlplane runs at startup, so a key the binary requires is never left
// out of the deployed ConfigMap. The deployment's environment takes its placeholders
// (environment.env), and values the Pod receives with no default, such as
// Secrets, take a stand-in.
func TestTheDeployedConfigurationIsValid(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	require.True(t, ok)
	manifest, err := os.ReadFile(filepath.Join(filepath.Dir(source), "../../deploy/kubernetes/workbench/workbench.yaml"))
	require.NoError(t, err)

	var defaults string
	decoder := yaml.NewDecoder(bytes.NewReader(manifest))
	for {
		var document struct {
			Kind     string                `yaml:"kind"`
			Metadata struct{ Name string } `yaml:"metadata"`
			Data     map[string]string     `yaml:"data"`
		}
		if decoder.Decode(&document) != nil {
			break
		}
		if document.Kind == "ConfigMap" && document.Metadata.Name == "controlplane-config" {
			defaults = document.Data["defaults.yml"]
		}
	}
	require.NotEmpty(t, defaults, "deploy/kubernetes/workbench declares ConfigMap controlplane-config")

	environment, err := os.ReadFile(filepath.Join(filepath.Dir(source), "../../deploy/kubernetes/workbench/environment.env"))
	require.NoError(t, err)
	for _, line := range strings.Split(string(environment), "\n") {
		key, value, found := strings.Cut(line, "=")
		if found && !strings.HasPrefix(line, "#") {
			t.Setenv(key, value)
		}
	}
	for _, match := range regexp.MustCompile(`\$\{([A-Z0-9_]+)(:[^}]*)?\}`).FindAllStringSubmatch(defaults, -1) {
		if _, set := os.LookupEnv(match[1]); !set && (match[2] == "" || match[2] == ":") {
			t.Setenv(match[1], "stand-in")
		}
	}

	directory := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(directory, "defaults.yml"), []byte(defaults), 0o600))
	var option Option
	require.NoError(t, loader.Load(&option, loader.WithConfigurationDirectory(directory, "defaults"), loader.WithConfigFilePrefix("controlplane")))
	require.NoError(t, validateOption(option))
}
