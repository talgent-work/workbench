package application

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// ExecutionThinkingPort forwards a setting to the admitted Harness. Native
// effect timing belongs to the Harness; AgentWork retains accepted settings.
type ExecutionThinkingPort interface {
	SetThinkingLevel(context.Context, string, string) error
}

type SetThinkingLevel struct{ ActorUserID, WorkID, Level string }

func (a *Application) GetThinkingLevel(ctx context.Context, input ControlWork) (string, error) {
	if err := a.WorkPrincipals.AuthorizeWork(ctx, input.ActorUserID, input.WorkID); err != nil {
		return "", err
	}
	session, err := a.Works.Get(ctx, input.WorkID)
	if err != nil {
		return "", err
	}
	return session.ThinkingLevel, nil
}

func (a *Application) SetThinkingLevel(ctx context.Context, input SetThinkingLevel) error {
	if err := a.WorkPrincipals.AuthorizeWork(ctx, input.ActorUserID, input.WorkID); err != nil {
		return err
	}
	session, err := a.Works.Get(ctx, input.WorkID)
	if err != nil {
		return err
	}
	if err := session.RequireReadyProcess(); err != nil {
		return err
	}
	// The Work's Model Configuration names the template whose levels apply;
	// the machine's Claude login offers every Harness level.
	if levels, defaultLevel, ok := a.Models.ThinkingLevels(session.WorkModel); ok && len(levels) > 0 && (input.Level == "" || slices.Contains(levels, input.Level)) {
		level := input.Level
		if level == "" {
			level = defaultLevel
		}
		if err := a.Thinking.SetThinkingLevel(ctx, session.ID, level); err != nil {
			return err
		}
		return a.retainThinkingLevel(ctx, session, level)
	}
	return fmt.Errorf("%w: thinking level is not available for this Work model", domain.ErrInvalidWork)
}

// Lifecycle feedback can advance the Work while native control is in flight.
// Retry only persistence, keeping that newer state and never replaying control or
// overwriting a competing setting. The setting is retained across process restarts.
func (a *Application) retainThinkingLevel(ctx context.Context, work *domain.AgentWork, level string) error {
	previous := work.ThinkingLevel
	for range 8 {
		if err := ctx.Err(); err != nil {
			return err
		}
		work.ThinkingLevel = level
		err := a.Works.Save(ctx, work)
		if !errors.Is(err, domain.ErrConcurrentModification) {
			return err
		}
		work, err = a.Works.Get(ctx, work.ID)
		if err != nil {
			return err
		}
		if work.ThinkingLevel == level {
			return nil
		}
		if work.ThinkingLevel != previous {
			return domain.ErrConcurrentModification
		}
	}
	return domain.ErrConcurrentModification
}
