package execution

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// Every start places the Workspace Instructions it took up at the workspace
// root, over whatever the file then holds. A start without any removes only
// the file an earlier start placed and the Work left unchanged, so
// instructions the Work wrote there itself stay.
func TestPreparationPlacesTheWorkspaceInstructionsOfEachStart(t *testing.T) {
	workspace := t.TempDir()
	file := filepath.Join(workspace, domain.ClaudeCode.InstructionsFile)
	starts := 0
	start := func(instructions string) string {
		starts++
		return runInstructionsPreparation(t, workspace, "run-"+strconv.Itoa(starts), instructions)
	}

	require.Equal(t, "placed", start("# Repositories\nRun make check.\n"))
	require.Equal(t, "# Repositories\nRun make check.\n", readFile(t, file))

	require.NoError(t, os.WriteFile(file, []byte("edited by the Work"), 0o600))
	require.Equal(t, "placed", start("Run make test."))
	require.Equal(t, "Run make test.", readFile(t, file), "the definition's instructions replace the Work's edit")

	require.Equal(t, "removed", start(""))
	require.NoFileExists(t, file)

	require.NoError(t, os.WriteFile(file, []byte("the Work's own"), 0o600))
	require.Equal(t, "absent", start(""))
	require.Equal(t, "the Work's own", readFile(t, file), "nothing placed it, so it stays")

	require.Equal(t, "placed", start("Run make lint."))
	require.NoError(t, os.WriteFile(file, []byte("rewritten by the Work"), 0o600))
	require.Equal(t, "absent", start(""))
	require.Equal(t, "rewritten by the Work", readFile(t, file), "changed since it was placed, so it stays")
	require.Equal(t, "absent", start(""))
	require.Equal(t, "rewritten by the Work", readFile(t, file))
}

// Instructions at their bound, in the widest characters, still travel inline
// in the one argument the preparation command is.
func TestPreparationCarriesWorkspaceInstructionsAtTheirBound(t *testing.T) {
	workspace := t.TempDir()
	instructions := strings.Repeat("𝄞", domain.MaxWorkspaceInstructionsRunes)
	require.Equal(t, "placed", runInstructionsPreparation(t, workspace, "bound", instructions))
	require.Equal(t, instructions, readFile(t, filepath.Join(workspace, domain.ClaudeCode.InstructionsFile)))
}

// Instructions that cannot be placed fail alone: the preparation still ends
// and reports, so the Harness starts.
func TestPreparationReportsWorkspaceInstructionsItCannotPlace(t *testing.T) {
	workspace := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(workspace, domain.ClaudeCode.InstructionsFile), 0o700))
	require.Equal(t, "failed", runInstructionsPreparation(t, workspace, "blocked", "Run make check."))
	entries, err := os.ReadDir(workspace)
	require.NoError(t, err)
	for _, entry := range entries {
		require.False(t, strings.Contains(entry.Name(), ".talgent-"), "no temporary file is left: %s", entry.Name())
	}
}

// runInstructionsPreparation runs one start's preparation command in a Cloud
// layout, with nothing to install or check out, and returns how its Workspace
// Instructions ended.
func runInstructionsPreparation(t *testing.T, workspace, allocation, instructions string) string {
	t.Helper()
	input := request()
	input.Capabilities.Plugins = nil
	input.WorkspaceInstructions = instructions
	launch, err := NativeLaunchConfig{Binary: "claude"}.prepare(sandboxLayout, input, SandboxOwner{WorkID: input.WorkID, ID: allocation}, "")
	require.NoError(t, err)
	home := t.TempDir()
	process := exec.Command("sh", "-c", launch.PreparationCommand)
	process.Dir = workspace
	process.Env = append(os.Environ(), "HOME="+home, "CLAUDE_CONFIG_DIR="+filepath.Join(home, ".claude"))
	output, err := process.Output()
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	var report preparationReport
	require.NoError(t, json.Unmarshal([]byte(lines[len(lines)-1]), &report), "%s", output)
	return report.Instructions
}
