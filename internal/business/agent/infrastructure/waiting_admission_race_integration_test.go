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

type beforeTimeoutSave struct {
	domain.AgentWorkRepository
	before func()
}

func (r beforeTimeoutSave) Save(ctx context.Context, work *domain.AgentWork) error {
	r.before()
	return r.AgentWorkRepository.Save(ctx, work)
}

func TestIdleTimeoutAndInputAcceptanceShareCommitOrder(t *testing.T) {
	for _, timeoutFirst := range []bool{true, false} {
		name := "input commits first"
		if timeoutFirst {
			name = "timeout commits first"
		}
		t.Run(name, func(t *testing.T) {
			f, ref := runningInputSession(t)
			ctx := t.Context()
			now := time.Now().UTC()
			origin := now.Add(-30 * time.Minute)
			feedback := newRunsAt(t, f.db, f.sessions, func() time.Time { return origin })
			// A Turn ending at origin leaves the Work idle since then.
			require.NoError(t, feedback.Handle(ctx, command.ExecutionFeedback{ProcessID: "process", WorkID: ref, Kind: command.TurnStarted, ObservationSequence: 1}))
			require.NoError(t, feedback.Handle(ctx, command.ExecutionFeedback{ProcessID: "process", WorkID: ref, Kind: command.TurnFinished, ObservationSequence: 2}))
			recover := command.NewRecoverWorkHandler(func() time.Time { return now }, f.sessions, &commandtest.InputBuffer{}, command.NewInputAcceptance(f.sessions, &commandtest.InputBuffer{}), &commandtest.InputSettlements{}, nil)
			input := command.ExecutionInput{ID: uuid.NewString(), Text: "new accepted task"}
			buffer := &commandtest.InputBuffer{}
			delivery := command.NewInputAcceptance(f.sessions, buffer)
			if timeoutFirst {
				// The timeout read the buffer before the append, so it stops the Work.
				racing := closingBuffer{InputBuffer: buffer, during: func() { require.NoError(t, recover.Handle(ctx, ref)) }}
				staleDelivery := command.NewInputAcceptance(f.sessions, racing)
				require.NoError(t, acceptance(staleDelivery.AcceptInput(ctx, ref, input)), "buffered input is accepted although its commit lost to the stop")
				require.Equal(t, []command.ExecutionInput{input}, buffer.Inputs, "it waits in the buffer for the next Resume")
				require.True(t, feedback.latest(ctx, ref).ExitRequest.Requested(), "the committed timeout stop asks the process to exit")
				work, err := f.sessions.Get(ctx, ref)
				require.NoError(t, err)
				require.Equal(t, domain.WorkStopping, work.State)
			} else {
				stale := beforeTimeoutSave{AgentWorkRepository: f.sessions, before: func() { require.NoError(t, acceptance(delivery.AcceptInput(ctx, ref, input))) }}
				staleRecovery := command.NewRecoverWorkHandler(func() time.Time { return now }, stale, &commandtest.InputBuffer{}, command.NewInputAcceptance(stale, &commandtest.InputBuffer{}), &commandtest.InputSettlements{}, nil)
				require.ErrorIs(t, staleRecovery.Handle(ctx, ref), domain.ErrConcurrentModification)
				require.False(t, feedback.latest(ctx, ref).ExitRequest.Requested(), "a rejected timeout cannot send a stop")
				require.Len(t, buffer.Inputs, 1)
				work, err := f.sessions.Get(ctx, ref)
				require.NoError(t, err)
				require.Equal(t, domain.WorkWorking, work.State)
				require.False(t, work.Idle(), "accepted input ends idleness")
				require.NoError(t, recover.Handle(ctx, ref))
				require.False(t, feedback.latest(ctx, ref).ExitRequest.Requested())
			}
		})
	}
}
