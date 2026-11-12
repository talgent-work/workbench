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
	w := runningWorld(t)
	session, store := w.work(), w.works
	session.PermissionMode = domain.PermissionPlan
	saves := store.saves
	handler := w.feedback()
	report := command.ExecutionFeedback{Kind: command.PermissionModeObserved, WorkID: session.ID, ProcessID: "earlier", ObservationSequence: 7, PermissionMode: domain.PermissionBypass}
	require.NoError(t, handler.Handle(context.Background(), report))
	require.Equal(t, domain.PermissionPlan, store.session.PermissionMode)
	require.Equal(t, saves, store.saves)

	report.ProcessID, report.PermissionMode = "process", domain.PermissionAcceptEdits
	require.NoError(t, handler.Handle(context.Background(), report))
	require.Equal(t, domain.PermissionAcceptEdits, store.session.PermissionMode)
	require.Equal(t, saves+1, store.saves)
}

// A Personal Work's current Harness process names the conversation's title,
// which the Work takes; a report of a replaced process changes nothing, and
// neither does a title the Work already has.
func TestAPersonalWorkTakesTheTitleItsCurrentProcessReports(t *testing.T) {
	w := runningWorld(t)
	session, store := w.work(), w.works
	session.Subject, session.ProjectID, session.DisplayTitle = domain.WorkSubject{}, "", "First line of the input"
	require.True(t, session.Personal())
	saves := store.saves
	handler := w.feedback()
	report := command.ExecutionFeedback{Kind: command.TitleObserved, WorkID: session.ID, ProcessID: "earlier", ObservationSequence: 7, Title: "Go context 包"}
	require.NoError(t, handler.Handle(context.Background(), report))
	require.Equal(t, "First line of the input", store.session.DisplayTitle)
	require.Equal(t, saves, store.saves)

	report.ProcessID = "process"
	require.NoError(t, handler.Handle(context.Background(), report))
	require.Equal(t, "Go context 包", store.session.DisplayTitle)
	require.Equal(t, saves+1, store.saves)
	require.NoError(t, handler.Handle(context.Background(), report))
	require.Equal(t, saves+1, store.saves)
}
