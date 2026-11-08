package domain_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

func harnessWork(t *testing.T, managed bool, choice domain.HarnessChoice) (*domain.AgentWork, *executionPort, error) {
	t.Helper()
	input := machineLoginInput(managed)
	if !managed {
		input.WorkModel = domain.WorkModelSelection{ConfigurationID: "glm-5"}
	}
	input.Harness = choice
	admission := seatedFor(input)
	admission.ModelConfigurationProvided = true
	work, err := domain.NewAgentWork(input, admission)
	port := follow(work)
	return work, port, err
}

// A Managed Work bypasses permission prompts unless its starter chose
// otherwise, and runs outside the sandbox unless asked.
func TestAManagedWorkStartsInItsChosenPermissionMode(t *testing.T) {
	work, port, err := harnessWork(t, true, domain.HarnessChoice{})
	require.NoError(t, err)
	require.Equal(t, domain.PermissionBypass, work.PermissionMode)
	require.Equal(t, domain.PermissionBypass, port.launch().PermissionMode)
	require.False(t, port.launch().Capabilities.Sandbox)

	work, port, err = harnessWork(t, true, domain.HarnessChoice{PermissionMode: domain.PermissionPlan, Sandbox: true})
	require.NoError(t, err)
	require.Equal(t, domain.PermissionPlan, work.PermissionMode)
	require.True(t, work.CapabilitySnapshot.Sandbox)
	require.True(t, port.launch().Capabilities.Sandbox)

	_, _, err = harnessWork(t, true, domain.HarnessChoice{PermissionMode: "auto"})
	require.ErrorIs(t, err, domain.ErrInvalidWork)
}

// A Cloud Work has no permission mode and no sandbox choice.
func TestACloudWorkChoosesNoHarnessSettings(t *testing.T) {
	work, port, err := harnessWork(t, false, domain.HarnessChoice{})
	require.NoError(t, err)
	require.Empty(t, work.PermissionMode)
	require.Empty(t, port.launch().PermissionMode)
	for _, choice := range []domain.HarnessChoice{{PermissionMode: domain.PermissionBypass}, {Sandbox: true}} {
		_, _, err := harnessWork(t, false, choice)
		require.ErrorIs(t, err, domain.ErrHarnessChoiceNotManaged)
	}
}

// runningHarnessWork is a Managed Work whose Harness is ready.
func runningHarnessWork(t *testing.T, choice domain.HarnessChoice) (*domain.AgentWork, *executionPort) {
	t.Helper()
	work, port, err := harnessWork(t, true, choice)
	require.NoError(t, err)
	ready(t, work)
	return work, port
}

// Only the person who started a running Managed Work asks for a mode a
// person may choose.
func TestOnlyTheStarterChangesAPermissionMode(t *testing.T) {
	work, _, err := harnessWork(t, true, domain.HarnessChoice{})
	require.NoError(t, err)
	require.ErrorIs(t, work.AdmitPermissionModeChange(work.StartedBy, domain.PermissionAsk), domain.ErrWorkTransition, "a Starting Work's Harness is not ready")

	work, _ = runningHarnessWork(t, domain.HarnessChoice{})
	require.NoError(t, work.AdmitPermissionModeChange(work.StartedBy, domain.PermissionAsk))
	require.ErrorIs(t, work.AdmitPermissionModeChange("another-owner", domain.PermissionAsk), domain.ErrPermissionModeStarterOnly)
	require.ErrorIs(t, work.AdmitPermissionModeChange("", domain.PermissionAsk), domain.ErrPermissionModeStarterOnly)
	require.ErrorIs(t, work.AdmitPermissionModeChange(work.StartedBy, domain.PermissionAuto), domain.ErrInvalidWork, "a mode the Harness only reports is not chosen")
	require.ErrorIs(t, domain.AdmitPermissionModeRequester("", ""), domain.ErrPermissionModeStarterOnly)
}

// The Work retains the latest mode its current process reports, including
// one the Harness changed to by itself, and every later start launches in
// it; the sandbox stays on across Resume.
func TestTheReportedPermissionModeHoldsAcrossResume(t *testing.T) {
	work, port := runningHarnessWork(t, domain.HarnessChoice{PermissionMode: domain.PermissionPlan, Sandbox: true})
	require.NoError(t, work.RecordPermissionMode(10, domain.PermissionAcceptEdits), "leaving plan mode once its plan was approved")
	require.NoError(t, work.RecordPermissionMode(9, domain.PermissionAsk), "an older report")
	require.NoError(t, work.RecordPermissionMode(10, domain.PermissionAsk), "a replayed position")
	require.Equal(t, domain.PermissionAcceptEdits, work.PermissionMode)
	require.NoError(t, work.RecordPermissionMode(11, domain.PermissionDontAsk), "a mode the Harness applies though no one chooses it")
	require.Equal(t, domain.PermissionDontAsk, work.PermissionMode)
	require.ErrorIs(t, work.RecordPermissionMode(12, "someFutureMode"), domain.ErrInvalidWork)
	require.ErrorIs(t, work.RecordPermissionMode(0, domain.PermissionAsk), domain.ErrInvalidWork)

	require.NoError(t, work.Stop("stop-1", false))
	exited(t, work, domain.HarnessExit{})
	require.NoError(t, work.RecordPermissionMode(20, domain.PermissionBypass), "an ended process reports nothing")
	require.Equal(t, domain.PermissionDontAsk, work.PermissionMode)
	released(t, work)
	require.NoError(t, work.Resume(seated(work.AgentID)))
	resumed := port.launch()
	require.Equal(t, domain.PermissionDontAsk, resumed.PermissionMode)
	require.True(t, resumed.Capabilities.Sandbox)
	// The new process's stream starts over: its first report counts.
	require.NoError(t, work.RecordPermissionMode(1, domain.PermissionPlan))
	require.Equal(t, domain.PermissionPlan, work.PermissionMode)
}

func TestACloudWorkAdmitsNoPermissionModeChange(t *testing.T) {
	work, _, err := harnessWork(t, false, domain.HarnessChoice{})
	require.NoError(t, err)
	ready(t, work)
	require.ErrorIs(t, work.AdmitPermissionModeChange(work.StartedBy, domain.PermissionAsk), domain.ErrHarnessChoiceNotManaged)
	require.NoError(t, work.RecordPermissionMode(5, domain.PermissionBypass), "a Cloud Harness's own report is not retained")
	require.Empty(t, work.PermissionMode)
}
