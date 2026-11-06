package domain

import (
	"slices"
	"strings"
	"time"
)

// WorkProgress is the latest complete Plan/TODO snapshot, independent of a turn
// or startup attempt. It has no identity or lifecycle of its own.
type WorkProgress struct {
	Items     []WorkProgressItem
	UpdatedAt time.Time
}

type WorkProgressItem struct {
	Content    string `validate:"required"`
	Status     string `validate:"oneof=pending in_progress completed"`
	ActiveForm string
}

func (p WorkProgress) Validate() error {
	if p.UpdatedAt.IsZero() {
		return ErrInvalidWork
	}
	for _, item := range p.Items {
		if validation.Struct(item) != nil || strings.TrimSpace(item.Content) == "" {
			return ErrInvalidWork
		}
	}
	return nil
}

// RecordWorkProgress replaces Progress with the supplied snapshot without
// attributing it to a process run. sequence is the snapshot's position in its
// process stream and only makes an exact replay a no-op; a late snapshot from
// an earlier run may cause only a transient error.
func (s *AgentWork) RecordWorkProgress(sequence int64, items []WorkProgressItem, observedAt time.Time) error {
	if sequence <= 0 {
		return ErrInvalidWork
	}
	if s.HarnessProcess == nil || sequence <= s.HarnessProcess.WorkProgressSequence {
		return nil
	}
	progress := WorkProgress{Items: slices.Clone(items), UpdatedAt: observedAt.UTC()}
	if err := progress.Validate(); err != nil {
		return err
	}
	s.Progress = &progress
	s.HarnessProcess.WorkProgressSequence = sequence
	return nil
}
