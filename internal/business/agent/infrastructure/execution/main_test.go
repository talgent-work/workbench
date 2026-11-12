package execution

import (
	"os"
	"os/exec"
	"testing"
)

// TestMain loads node and git once before any case runs. The first start of
// each on a cold CI runner can take seconds, which would land in whichever
// case happens to run them first and push it over its time budget; a case's
// time is then the cost of its own rule. Tools that are missing are left to
// the cases, which skip without them.
func TestMain(m *testing.M) {
	for _, tool := range []string{"node", "git"} {
		_ = exec.Command(tool, "--version").Run()
	}
	os.Exit(m.Run())
}
