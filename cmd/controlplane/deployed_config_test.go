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
	var pod deployedPod
	decoder := yaml.NewDecoder(bytes.NewReader(manifest))
	for {
		var document struct {
			Kind     string                `yaml:"kind"`
			Metadata struct{ Name string } `yaml:"metadata"`
			Data     map[string]string     `yaml:"data"`
			Spec     struct {
				Template struct {
					Spec deployedPod `yaml:"spec"`
				} `yaml:"template"`
			} `yaml:"spec"`
		}
		if decoder.Decode(&document) != nil {
			break
		}
		if document.Kind == "ConfigMap" && document.Metadata.Name == "controlplane-config" {
			defaults = document.Data["defaults.yml"]
		}
		if document.Kind == "Deployment" && document.Metadata.Name == "controlplane" {
			pod = document.Spec.Template.Spec
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
	require.Equal(t, loadDefaults(t).Marketplace.Providers["github"].Targets, option.Marketplace.Providers["github"].Targets,
		"the deployment binds GitHub's credentials exactly as the base does")
	require.Equal(t, "agent-personal-variable-key", pod.secretFile(option.AgentPersonalVariables.KeyPath),
		"the Personal Variable key is the file the controlplane mounts from its Secret")
}

// deployedPod is what the controlplane Pod mounts.
type deployedPod struct {
	Containers []struct {
		VolumeMounts []struct {
			MountPath string `yaml:"mountPath"`
			Name      string `yaml:"name"`
		} `yaml:"volumeMounts"`
	} `yaml:"containers"`
	Volumes []struct {
		Name   string `yaml:"name"`
		Secret struct {
			SecretName string `yaml:"secretName"`
			Items      []struct {
				Path string `yaml:"path"`
			} `yaml:"items"`
		} `yaml:"secret"`
	} `yaml:"volumes"`
}

// secretFile names the Secret whose item the Pod mounts at path, or "".
func (p deployedPod) secretFile(path string) string {
	directory, name := filepath.Dir(path), filepath.Base(path)
	for _, container := range p.Containers {
		for _, mount := range container.VolumeMounts {
			if mount.MountPath != directory {
				continue
			}
			for _, volume := range p.Volumes {
				if volume.Name != mount.Name {
					continue
				}
				for _, item := range volume.Secret.Items {
					if item.Path == name {
						return volume.Secret.SecretName
					}
				}
			}
		}
	}
	return ""
}
