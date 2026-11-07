package domain_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"
)

func workWithInitialTaskInput(t *testing.T, input *domain.InitialTaskInput) *domain.AgentWork {
	t.Helper()
	agent, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"owner"}, Purpose: "work", UsageScope: domain.UsageScope{Kind: "employee", Reference: "employee"}, HomeBinding: domain.HomeBinding{OwnerID: "user-home", Namespace: "project"}})
	require.NoError(t, err)
	work, err := domain.NewAgentWork(context.Background(), domain.WorkInput{WorkModel: domain.WorkModelSelection{ConfigurationID: "default"}, StartedBy: "owner", ID: uuid.Must(uuid.NewV7()).String(),
		AgentID: agent.ID, Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}}, WorkspaceReference: "workspace-a",
		HomeBinding: domain.HomeBinding{OwnerID: "user-home", Namespace: "project"}, InitialTaskInput: input,
	}, domain.StartAdmission{AdmittedAt: time.Now(), AgentID: agent.ID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", DeskID: "desk-test"}, ModelConfigurationProvided: true}, &executionPort{})
	require.NoError(t, err)
	return work
}

func TestInitialTaskInputRequiresIdentityAndText(t *testing.T) {
	_, err := domain.NewInitialTaskInput("not-a-uuid", "Do the work")
	require.ErrorIs(t, err, domain.ErrInvalidWork)
	_, err = domain.NewInitialTaskInput(uuid.NewString(), "  ")
	require.ErrorIs(t, err, domain.ErrInvalidWork)
}

// A new Work holds its initial task input until it reaches the input buffer;
// no later input or answer is accepted ahead of it (Initial task input order).
func TestInitialTaskInputIsHandedOffBeforeAnyLaterInput(t *testing.T) {
	initial, err := domain.NewInitialTaskInput(uuid.NewString(), "Do the work")
	require.NoError(t, err)
	work := workWithInitialTaskInput(t, &initial)

	pending, ok := work.PendingInitialTaskInput()
	require.True(t, ok)
	require.Equal(t, initial, pending)
	require.ErrorIs(t, work.AcceptInput(false), domain.ErrInitialTaskInputPending)
	require.ErrorIs(t, work.AcceptResponse(false), domain.ErrInitialTaskInputPending)

	require.False(t, work.HandOffInitialTaskInput(uuid.NewString()), "another input identity hands off nothing")
	_, ok = work.PendingInitialTaskInput()
	require.True(t, ok)

	require.True(t, work.HandOffInitialTaskInput(initial.ID))
	_, ok = work.PendingInitialTaskInput()
	require.False(t, ok, "a handed-off initial task input is no longer held")
	require.False(t, work.HandOffInitialTaskInput(initial.ID), "a repeated hand-off changes nothing")
	require.NoError(t, work.AcceptInput(false), "later input follows the handed-off initial task input")
}

func TestWorkWithoutInitialTaskInputAcceptsInputAtOnce(t *testing.T) {
	work := workWithInitialTaskInput(t, nil)
	_, ok := work.PendingInitialTaskInput()
	require.False(t, ok)
	require.NoError(t, work.AcceptInput(false))
}

// A running Work whose initial task input has not reached its buffer still
// owes it, so a close waits for the hand-off.
func TestRunningWorkHoldingItsInitialTaskInputIsNotClosed(t *testing.T) {
	ctx := context.Background()
	initial, err := domain.NewInitialTaskInput(uuid.NewString(), "Do the work")
	require.NoError(t, err)
	work := workWithInitialTaskInput(t, &initial)
	port := &executionPort{}
	require.NoError(t, recoverStartup(ctx, work, port))
	require.NoError(t, work.ConfirmPrepared(ctx, time.Now(), nil))
	require.NoError(t, work.ConfirmStarted(ctx, time.Now(), nil))
	require.ErrorIs(t, work.Close(ctx, "finish", domain.ClosureFacts{}, port), domain.ErrWorkTransition)
	require.True(t, work.HandOffInitialTaskInput(initial.ID))
	require.NoError(t, work.Close(ctx, "finish", domain.ClosureFacts{}, port))
	require.Equal(t, domain.WorkStopping, work.State)
}

// Closing empties input: an initial task input a failed first start never
// handed off is dropped when the Work closes.
func TestClosingDropsAnInitialTaskInputNotHandedOff(t *testing.T) {
	ctx := context.Background()
	initial, err := domain.NewInitialTaskInput(uuid.NewString(), "Do the work")
	require.NoError(t, err)
	work := workWithInitialTaskInput(t, &initial)
	port := &executionPort{}
	failed, err := work.FailAtStartupDeadline(ctx, time.Now().Add(domain.StartupDeadline+time.Hour), port)
	require.NoError(t, err)
	require.True(t, failed)
	require.NoError(t, work.Close(ctx, "close", domain.ClosureFacts{}, port))
	_, pending := work.PendingInitialTaskInput()
	require.False(t, pending)
}
