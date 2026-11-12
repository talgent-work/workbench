package execution

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// Preparation (Plugins, then repository checkout) has no limit of its own:
// when it runs, it gets what is left until the bound the Work supplies.
func TestPreparationIsBoundedByTheStartupDeadline(t *testing.T) {
	for name, tc := range map[string]struct {
		bound time.Duration
		want  int
	}{
		"fresh start":    {bound: 13 * time.Minute, want: 780},
		"late start":     {bound: 3 * time.Minute, want: 180},
		"past the bound": {bound: -5 * time.Minute, want: 30},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			// A stand-in timeout reports the budget it was given instead of running.
			require.NoError(t, os.WriteFile(filepath.Join(dir, "timeout"), []byte("#!/bin/sh\necho \"$3 ${11}\"\n"), 0o755))
			input := request()
			input.PreparationBound = time.Now().Add(tc.bound)
			input.Capabilities.Plugins = []domain.SelectedPlugin{{Release: domain.PluginRelease{CatalogName: "talgent", Slug: "fixed", Source: "fixture", Version: "1", RetrievalRef: "archive:fixed", NativeEntry: []byte(`{"name":"fixed","version":"1"}`)}}}
			launch, err := NativeLaunchConfig{Binary: "claude"}.prepare(sandboxLayout, input, SandboxOwner{WorkID: input.WorkID, ID: fixtureAllocationID}, "")
			require.NoError(t, err)
			require.NotContains(t, launch.PreparationCommand, "600s")

			process := exec.Command("sh", "-c", launch.PreparationCommand)
			process.Env = append(os.Environ(), "HOME="+dir, "CLAUDE_CONFIG_DIR="+filepath.Join(dir, ".claude"), "PATH="+dir+":"+os.Getenv("PATH"))
			output, err := process.Output()
			require.NoError(t, err)
			fields := strings.Fields(string(output))
			require.Len(t, fields, 2)
			timeout, err := strconv.Atoi(strings.TrimSuffix(fields[0], "s"))
			require.NoError(t, err)
			lock, err := strconv.Atoi(fields[1])
			require.NoError(t, err)
			require.InDelta(t, tc.want, timeout, 2)
			require.Equal(t, timeout, lock, "lock waiting and checkout share the same budget")
		})
	}
}
