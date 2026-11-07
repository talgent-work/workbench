package application_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

type disconnectedView struct{ view query.WorkView }

func (v disconnectedView) GetWorkView(context.Context, string) (query.WorkView, error) {
	return v.view, nil
}

// unreachedTurns fails the test if a control or read reaches Dispatcher.
type unreachedTurns struct {
	command.RunningHarnessPort
	t *testing.T
}

func (u unreachedTurns) CancelTurn(context.Context, string, string) error {
	u.t.Fatal("a disconnected Work's Turn is not cancelled through Dispatcher")
	return nil
}
func (u unreachedTurns) GetCurrentTurn(context.Context, string) (command.CurrentTurn, error) {
	u.t.Fatal("a disconnected Work's Turn is not read through Dispatcher")
	return command.CurrentTurn{}, nil
}
func (u unreachedTurns) ListPendingInteractions(context.Context, string) ([]command.PendingInteraction, error) {
	u.t.Fatal("a disconnected Work's requests are not read through Dispatcher")
	return nil, nil
}

// During a Disconnection the live Turn and the Harness's requests are not
// observed: their reads and the Turn's cancellation answer the expected
// ErrExecutionUnobserved without asking Dispatcher.
func TestLiveTurnControlsAreUnobservedDuringADisconnection(t *testing.T) {
	ctx := context.Background()
	since := time.Now().Add(-time.Minute)
	app, store, _, _ := thinkingApplication()
	store.work.DisconnectedSince = since
	app.WorkViews = disconnectedView{view: query.WorkView{WorkID: "work", State: "working", DisconnectedSince: since}}
	app.Harness = unreachedTurns{t: t}
	work := application.ControlWork{ActorUserID: "owner", WorkID: "work"}

	_, err := app.GetCurrentTurn(ctx, work)
	require.ErrorIs(t, err, domain.ErrExecutionUnobserved)
	_, err = app.ListPendingInteractions(ctx, work)
	require.ErrorIs(t, err, domain.ErrExecutionUnobserved)
	require.ErrorIs(t, app.CancelTurn(ctx, application.CancelTurn{ActorUserID: "owner", WorkID: "work", TurnID: "turn"}), domain.ErrExecutionUnobserved)
}
