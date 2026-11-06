package command_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// A Managed Work retains the mode its current Harness process reports, such
// as leaving plan mode by itself; a report of a process a Stop or Resume
// replaced changes nothing.
func TestAWorkRetainsThePermissionModeItsCurrentProcessReports(t *testing.T) {
	session := runningSession(t)
	session.PermissionMode = domain.PermissionPlan
	store := &feedbackSessionStore{session: session}
	handler := feedbackHandler(store, &observedProcess{observation: domain.ProcessObservation{State: domain.ProcessRunning}}, &feedbackPort{})
	report := command.ExecutionFeedback{Kind: command.PermissionModeObserved, WorkID: session.ID, ProcessID: "earlier", ObservationSequence: 7, PermissionMode: domain.PermissionBypass}
	require.NoError(t, handler.Handle(context.Background(), report))
	require.Equal(t, domain.PermissionPlan, store.session.PermissionMode)
	require.Zero(t, store.saves)

	report.ProcessID, report.PermissionMode = "process", domain.PermissionAcceptEdits
	require.NoError(t, handler.Handle(context.Background(), report))
	require.Equal(t, domain.PermissionAcceptEdits, store.session.PermissionMode)
	require.Equal(t, 1, store.saves)
}
