package execution

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

func managedLaunch(t *testing.T, mode domain.PermissionMode, sandbox bool) nativeLaunch {
	t.Helper()
	input := request()
	input.ComputerID = managedComputerID
	input.PermissionMode = mode
	input.Capabilities.Sandbox = sandbox
	launch, err := (NativeLaunchConfig{Binary: "claude"}).prepare(machineLayout, input, owner(input), "")
	require.NoError(t, err)
	return launch
}

// A Managed Work launches in its own permission mode; one that does not
// bypass permission prompts is launched so it may switch to bypassing later.
func TestManagedLaunchUsesTheWorksPermissionMode(t *testing.T) {
	require.Contains(t, managedLaunch(t, domain.PermissionBypass, false).Command, "'--permission-mode' 'bypassPermissions' '--system-prompt'")
	plan := managedLaunch(t, domain.PermissionPlan, false).Command
	require.Contains(t, plan, "'--permission-mode' 'plan' '--allow-dangerously-skip-permissions'")
	require.NotContains(t, plan, "bypassPermissions")
	require.Contains(t, managedLaunch(t, domain.PermissionAsk, false).Command, "'--permission-mode' 'default'")
}

// A sandboxed Work runs in Claude Code's sandbox or not at all, and no
// command may leave it; an unsandboxed one asks for no sandbox.
func TestManagedLaunchRequiresTheSandboxOfASandboxedWork(t *testing.T) {
	require.Contains(t, managedLaunch(t, domain.PermissionBypass, true).Command, `"sandbox":{"allowUnsandboxedCommands":false,"enabled":true,"failIfUnavailable":true}`)
	require.NotContains(t, managedLaunch(t, domain.PermissionBypass, false).Command, `"sandbox"`)
}

// A Cloud launch is unchanged: it bypasses permission prompts, and a Cloud
// request carrying a Managed choice is refused.
func TestCloudLaunchAlwaysBypassesAndRefusesAManagedChoice(t *testing.T) {
	input := request()
	launch, err := (NativeLaunchConfig{Binary: "claude"}).prepare(sandboxLayout, input, owner(input), "")
	require.NoError(t, err)
	require.Contains(t, launch.Command, "'--permission-mode' 'bypassPermissions' '--system-prompt'")
	require.NotContains(t, launch.Command, "--allow-dangerously-skip-permissions")
	require.NotContains(t, launch.Command, `"sandbox"`)

	input.PermissionMode = domain.PermissionPlan
	_, err = (NativeLaunchConfig{Binary: "claude"}).prepare(sandboxLayout, input, owner(input), "")
	require.ErrorIs(t, err, domain.ErrExecutionRequest)
	input.PermissionMode, input.Capabilities.Sandbox = "", true
	_, err = (NativeLaunchConfig{Binary: "claude"}).prepare(sandboxLayout, input, owner(input), "")
	require.ErrorIs(t, err, domain.ErrExecutionRequest)
}
