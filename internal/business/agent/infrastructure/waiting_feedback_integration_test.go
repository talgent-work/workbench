//go:build integration

package infrastructure_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// currentProcessFacts is Dispatcher's current observation of the Work's
// launched process, beside its input buffer.
type currentProcessFacts struct {
	*commandtest.InputBuffer
	command.WorkProcessObservation
}

func (p *currentProcessFacts) ObserveProcess(context.Context, string, string) (command.WorkProcessObservation, error) {
	return p.WorkProcessObservation, nil
}

func TestCurrentProcessRestoresIdlenessAfterLostInputAndReplyFeedback(t *testing.T) {
	f, id := runningInputSession(t)
	ctx := t.Context()
	now := time.Now().UTC()
	buffer := &commandtest.InputBuffer{}
	process := &currentProcessFacts{InputBuffer: buffer, WorkProcessObservation: command.WorkProcessObservation{
		Running: true, Ready: true,
		Turn: domain.TurnObservation{Sequence: 30, Completed: true}, PhaseSequence: 30,
	}}
	feedback := newRunsWith(t, f.db, f.sessions, func() time.Time { return now }, process)
	delivery := command.NewInputAcceptance(f.sessions, buffer)
	first := command.ExecutionInput{ID: uuid.NewString(), Text: "first task"}
	require.NoError(t, acceptance(delivery.AcceptInput(ctx, id, first)))
	require.NoError(t, delivery.AcceptResponse(ctx, id, command.InteractionResponse{InteractionID: "question", Allow: true}))
	observe := func() *domain.AgentWork {
		t.Helper()
		require.NoError(t, feedback.Handle(ctx, command.ExecutionFeedback{ProcessID: "process", WorkID: id, Kind: command.ProcessObserved}))
		work, err := f.sessions.Get(ctx, id)
		require.NoError(t, err)
		return work
	}
	require.False(t, observe().Idle(), "buffered input awaits processing")
	buffer.Settle(first.ID)
	require.False(t, observe().Idle(), "the reply remains buffered after the ordinary input settled")
	buffer.Settle("question")
	work := observe()
	require.True(t, work.Idle(), "the current buffer recovers lost settlement feedback")
	idleSince, version := work.HarnessProcess.IdleSince, work.Version
	now = now.Add(time.Minute)
	work = observe()
	require.Equal(t, idleSince, work.HarnessProcess.IdleSince)
	require.Equal(t, version, work.Version, "unchanged periodic observations must not invalidate user controls")
	second := command.ExecutionInput{ID: uuid.NewString(), Text: "next task"}
	require.NoError(t, acceptance(delivery.AcceptInput(ctx, id, second)))
	require.False(t, observe().Idle(), "old turn snapshot must not undo new acceptance")
	process.Turn = domain.TurnObservation{Sequence: 50, Completed: true}
	process.PhaseSequence = 50
	buffer.Settle(second.ID)
	work = observe()
	require.True(t, work.Idle())
	require.True(t, work.HarnessProcess.IdleSince.After(idleSince))
}
