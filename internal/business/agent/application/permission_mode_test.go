package application_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

type permissionHarness struct {
	command.RunningHarnessPort
	calls []domain.PermissionMode
	err   error
}

func (h *permissionHarness) SetPermissionMode(_ context.Context, _ string, mode domain.PermissionMode) (domain.PermissionMode, error) {
	h.calls = append(h.calls, mode)
	return mode, h.err
}

func permissionApplication(mode domain.PermissionMode) (*application.Application, *thinkingStore, *permissionHarness) {
	store := &thinkingStore{work: domain.AgentWork{ID: "work", StartedBy: "starter", State: domain.WorkWorking, PermissionMode: mode, HarnessProcess: &domain.HarnessProcess{}}}
	harness := &permissionHarness{}
	return &application.Application{Works: store, WorkPrincipals: &thinkingAccess{}, Harness: harness}, store, harness
}

// A running Managed Work's starter switches it at once and is answered the
// mode the Harness accepted. The Work retains it only from its Harness
// process's own report, so nothing is saved here and an answer from a
// process a Stop or Resume replaced cannot overwrite the Work.
func TestTheStarterSwitchesARunningManagedWork(t *testing.T) {
	app, store, harness := permissionApplication(domain.PermissionBypass)
	mode, err := app.SetPermissionMode(context.Background(), application.SetPermissionMode{ActorUserID: "starter", WorkID: "work", Mode: domain.PermissionPlan})
	require.NoError(t, err)
	require.Equal(t, domain.PermissionPlan, mode)
	require.Equal(t, []domain.PermissionMode{domain.PermissionPlan}, harness.calls)
	require.Zero(t, store.saves)
}

// Nothing reaches the Harness for another Owner of the definition, a Cloud
// Work or a stopped one, and a refused change keeps the previous mode.
func TestAPermissionModeChangeIsRefusedWithoutReachingTheHarness(t *testing.T) {
	app, _, harness := permissionApplication(domain.PermissionBypass)
	_, err := app.SetPermissionMode(context.Background(), application.SetPermissionMode{ActorUserID: "another-owner", WorkID: "work", Mode: domain.PermissionPlan})
	require.ErrorIs(t, err, domain.ErrPermissionModeStarterOnly)
	require.Empty(t, harness.calls)

	app, _, harness = permissionApplication("")
	_, err = app.SetPermissionMode(context.Background(), application.SetPermissionMode{ActorUserID: "starter", WorkID: "work", Mode: domain.PermissionPlan})
	require.ErrorIs(t, err, domain.ErrHarnessChoiceNotManaged)
	require.Empty(t, harness.calls)

	app, store, harness := permissionApplication(domain.PermissionBypass)
	store.work.State = domain.WorkStopped
	_, err = app.SetPermissionMode(context.Background(), application.SetPermissionMode{ActorUserID: "starter", WorkID: "work", Mode: domain.PermissionPlan})
	require.ErrorIs(t, err, domain.ErrWorkTransition)
	require.Empty(t, harness.calls)

	app, store, harness = permissionApplication(domain.PermissionBypass)
	harness.err = errors.New("refused")
	_, err = app.SetPermissionMode(context.Background(), application.SetPermissionMode{ActorUserID: "starter", WorkID: "work", Mode: domain.PermissionPlan})
	require.Error(t, err)
	require.Equal(t, domain.PermissionBypass, store.work.PermissionMode)
	require.Zero(t, store.saves)
}
