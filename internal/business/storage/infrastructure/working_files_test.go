package infrastructure

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

// Run as root (CI does, through sudo): it reproduces the controlplane
// preparing a space and the sandbox workload writing it as UID/GID 1000.
func TestPreparedSpacesAreWritableByTheSandboxUser(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root to exercise the controlplane-to-sandbox UID boundary")
	}
	root, err := os.MkdirTemp("", "storage-working-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	require.NoError(t, os.Chmod(root, 0o755))
	working := NewWorkingFiles(root, 1000)
	workspace, err := working.PrepareWorkspace(t.Context(), "ws-1", []string{"guides"})
	require.NoError(t, err)
	home, err := working.PrepareHome(t.Context(), "owner-key")
	require.NoError(t, err)
	write := func() {
		t.Helper()
		cmd := exec.Command("sh", "-c", `printf history >> "$1/transcript"; printf result >> "$2/result"`, "sandbox-write", filepath.Join(root, home), filepath.Join(root, workspace))
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 1000, Gid: 1000}}
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, string(output))
	}
	write()
	// A restart prepares the same spaces and keeps their content.
	_, err = working.PrepareWorkspace(t.Context(), "ws-1", []string{"guides"})
	require.NoError(t, err)
	_, err = working.PrepareHome(t.Context(), "owner-key")
	require.NoError(t, err)
	write()
	content, err := os.ReadFile(filepath.Join(root, home, "transcript"))
	require.NoError(t, err)
	require.Equal(t, "historyhistory", string(content))
	info, err := os.Stat(filepath.Join(root, workspace, "inputs"))
	require.NoError(t, err)
	require.True(t, info.IsDir(), "inputs/ is the mount point of the Attachments")
	info, err = os.Stat(filepath.Join(root, workspace, "guides"))
	require.NoError(t, err)
	require.True(t, info.IsDir(), "each Reference Space mount has its mount point")
}
