//go:build integration

package infrastructure_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"github.com/talgent-work/workbench/internal/testsupport/computertest"
)

const fixtureManagedComputer = "01950000-0000-7000-8000-00000000ac01"

// managedAdmission lists only the person's Managed Computer and seats every
// run there.
type managedAdmission struct{ command.ComputerPort }

func (managedAdmission) ListUsableComputers(context.Context, string) ([]command.UsableComputer, error) {
	return []command.UsableComputer{{ID: fixtureManagedComputer}}, nil
}

// AdmitRun answers as the Managed Computer it seats every run on.
func (m managedAdmission) AdmitRun(ctx context.Context, run command.RunAdmission) (command.ComputerAnswer, error) {
	answer, err := m.ComputerPort.AdmitRun(ctx, run)
	if answer.Kind == command.ComputerSeated {
		answer.Computer = domain.ComputerManaged
	}
	return answer, err
}

func TestTheMachineLoginSurvivesReloadAndIsShownOnTheWork(t *testing.T) {
	f := newWorkCreationFixture(t)
	handler := command.NewCreateWorkHandler(time.Now, f.agents, f.sessions, access.NewWorkAuthorizer(infrastructure.NewWorkAccessReader(f.db)), creationModels(), commandtest.StartCatalog(&creationCatalog{}, creationConnections{}), managedAdmission{computertest.Checked(startupAuthority{})}, creationPreparation(f.db, creationModels()))
	creation := f.request
	creation.ComputerID, creation.WorkModel = fixtureManagedComputer, domain.MachineLoginModel()
	id, err := handler.Handle(t.Context(), creation)
	require.NoError(t, err)
	work, err := f.sessions.Get(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, domain.MachineLoginModel(), work.WorkModel)
	require.Equal(t, fixtureManagedComputer, work.ComputerID)
	require.Equal(t, domain.ComputerManaged, work.ComputerKind, "the Computer kind survives reload")
	view, err := infrastructure.NewWorkQueryRepository(f.db).GetWorkView(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, domain.MachineLoginModel(), view.WorkModel)
}
