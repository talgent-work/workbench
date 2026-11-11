package plugins

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// corePlugin is the core plugin every Work runs.
const corePlugin = "talgent"

const productContextHook = corePlugin + "/hooks/product-context.mjs"

// Every Work starts with the core product, and only with it: the core plugin
// knows no Project, so neither a knowledge mount nor a Project changes it.
func TestEveryWorkStartsWithTheCoreProduct(t *testing.T) {
	work := t.TempDir()
	core := runHook(t, corePlugin, productContextHook, "TALGENT_WORK_ROOT="+work)
	if !strings.HasPrefix(core, "# Talgent\n") {
		t.Fatalf("a Work gets no core product:\n%s", core)
	}
	withProject := runHook(t, corePlugin, productContextHook, "TALGENT_WORK_ROOT="+work, "TALGENT_KNOWLEDGE_ROOT="+work, "TALGENT_PROJECT_ID=project")
	if withProject != core {
		t.Fatalf("a knowledge mount and a Project change the core product:\n%s", withProject)
	}
}

// runHook runs a SessionStart hook of a plugin with env alone.
func runHook(t *testing.T, root, hook string, env ...string) string {
	t.Helper()
	plugin, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", hook)
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "CLAUDE_PLUGIN_ROOT=" + plugin}, env...)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%s failed: %v", hook, err)
	}
	return string(out)
}
