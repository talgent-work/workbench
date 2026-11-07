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
			port := &recoveryExecutionPort{}
			factory := port
			feedback := command.NewApplyExecutionFeedbackHandler(func() time.Time { return origin }, f.sessions, exitedProcess, factory, &commandtest.InputBuffer{}, &commandtest.InputSettlements{})
			// A Turn ending at origin leaves the Work idle since then.
			require.NoError(t, feedback.Handle(ctx, command.ExecutionFeedback{ProcessID: "process", WorkID: ref, Kind: command.TurnStarted, ObservationSequence: 1}))
			require.NoError(t, feedback.Handle(ctx, command.ExecutionFeedback{ProcessID: "process", WorkID: ref, Kind: command.TurnFinished, ObservationSequence: 2}))
			recover := command.NewRecoverWorkHandler(func() time.Time { return now }, f.sessions, factory, &commandtest.InputBuffer{}, command.NewInputAcceptance(f.sessions, &commandtest.InputBuffer{}), &commandtest.InputSettlements{}, nil, nil)
			input := command.ExecutionInput{ID: uuid.NewString(), Text: "new accepted task"}
			buffer := &commandtest.InputBuffer{}
			delivery := command.NewInputAcceptance(f.sessions, buffer)
			if timeoutFirst {
				// The timeout read the buffer before the append, so it stops the Work.
				racing := closingBuffer{InputBuffer: buffer, during: func() { require.NoError(t, recover.Handle(ctx, ref)) }}
				staleDelivery := command.NewInputAcceptance(f.sessions, racing)
				require.NoError(t, acceptance(staleDelivery.AcceptInput(ctx, ref, input)), "buffered input is accepted although its commit lost to the stop")
				require.Equal(t, []command.ExecutionInput{input}, buffer.Inputs, "it waits in the buffer for the next Resume")
				require.Len(t, port.stops, 1)
				work, err := f.sessions.Get(ctx, ref)
				require.NoError(t, err)
				require.Equal(t, domain.WorkStopping, work.State)
			} else {
				stale := beforeTimeoutSave{AgentWorkRepository: f.sessions, before: func() { require.NoError(t, acceptance(delivery.AcceptInput(ctx, ref, input))) }}
				staleRecovery := command.NewRecoverWorkHandler(func() time.Time { return now }, stale, factory, &commandtest.InputBuffer{}, command.NewInputAcceptance(stale, &commandtest.InputBuffer{}), &commandtest.InputSettlements{}, nil, nil)
				require.ErrorIs(t, staleRecovery.Handle(ctx, ref), domain.ErrConcurrentModification)
				require.Empty(t, port.stops, "a rejected timeout cannot send a stop")
				require.Len(t, buffer.Inputs, 1)
				work, err := f.sessions.Get(ctx, ref)
				require.NoError(t, err)
				require.Equal(t, domain.WorkWorking, work.State)
				require.False(t, work.Idle(), "accepted input ends idleness")
				require.NoError(t, recover.Handle(ctx, ref))
				require.Empty(t, port.stops)
			}
		})
	}
}
