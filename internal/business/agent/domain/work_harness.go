package domain

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// PermissionMode is a Work's Harness permission mode, in the Harness's own
// vocabulary. Only a Work on a Managed Computer has one; a Cloud Work always
// bypasses permission prompts (Agent model: Managed permission mode).
type PermissionMode string

const (
	// PermissionAsk asks before every action the Harness's rules do not allow.
	PermissionAsk PermissionMode = "default"
	// PermissionAcceptEdits edits files without asking.
	PermissionAcceptEdits PermissionMode = "acceptEdits"
	// PermissionPlan plans without changing anything until its plan is
	// approved.
	PermissionPlan PermissionMode = "plan"
	// PermissionBypass acts without asking, the default on a Managed Computer
	// and how every Cloud Work runs.
	PermissionBypass PermissionMode = "bypassPermissions"
	// PermissionDontAsk denies what the Harness's rules do not allow, without
	// asking. The Harness may report it; a person does not choose it.
	PermissionDontAsk PermissionMode = "dontAsk"
	// PermissionAuto lets the Harness's own classifier decide what to ask. The
	// Harness may report it; a person does not choose it.
	PermissionAuto PermissionMode = "auto"
)

// PermissionModes are the modes a person may choose for a Managed Work.
var PermissionModes = []PermissionMode{PermissionAsk, PermissionAcceptEdits, PermissionPlan, PermissionBypass}

// HarnessPermissionModes are every mode the Harness applies and reports. A
// Work retains whichever its Harness reports, so a later start launches in
// it, though a person chooses only among PermissionModes.
var HarnessPermissionModes = append(slices.Clone(PermissionModes), PermissionDontAsk, PermissionAuto)

func (m PermissionMode) choosable() bool { return slices.Contains(PermissionModes, m) }

func (m PermissionMode) known() bool { return slices.Contains(HarnessPermissionModes, m) }

// ErrPermissionModeStarterOnly refuses a permission mode change requested by
// anyone but the person who started the Work.
var ErrPermissionModeStarterOnly = errors.New("agent: only the person who started the Work may change its permission mode")

// AdmitPermissionModeRequester admits the person who started the Work, and
// no one else, to change its permission mode.
func AdmitPermissionModeRequester(startedBy, requester string) error {
	if strings.TrimSpace(startedBy) == "" || requester != startedBy {
		return ErrPermissionModeStarterOnly
	}
	return nil
}

// ErrHarnessChoiceNotManaged refuses a permission mode or the sandbox for a
// Work that is not on a Managed Computer.
var ErrHarnessChoiceNotManaged = fmt.Errorf("%w: permission mode and sandbox are chosen only on a Managed Computer", ErrInvalidWork)

// HarnessChoice is what the person starting a Work chooses for its Harness:
// its permission mode and whether it runs in Claude Code's sandbox. Both are
// chosen only on a Managed Computer.
type HarnessChoice struct {
	PermissionMode PermissionMode
	Sandbox        bool
}

// On is the choice a new Work keeps on its Computer: on a Managed Computer
// the chosen mode, bypassing permission prompts when none was chosen, and
// the chosen sandbox; on a Cloud nothing, and choosing anything there is
// refused.
func (c HarnessChoice) On(managed bool) (HarnessChoice, error) {
	if !managed {
		if c != (HarnessChoice{}) {
			return HarnessChoice{}, ErrHarnessChoiceNotManaged
		}
		return HarnessChoice{}, nil
	}
	if c.PermissionMode == "" {
		c.PermissionMode = PermissionBypass
	}
	if !c.PermissionMode.choosable() {
		return HarnessChoice{}, fmt.Errorf("%w: unknown permission mode", ErrInvalidWork)
	}
	return c, nil
}

// validateHarness keeps a Work's Harness facts consistent: a permission mode
// is one the Harness applies, and only a Work with one — a Managed Work — runs
// in the sandbox.
func (s *AgentWork) validateHarness() error {
	if s.PermissionMode != "" && !s.PermissionMode.known() {
		return fmt.Errorf("%w: unknown permission mode", ErrInvalidWork)
	}
	if s.CapabilitySnapshot.Sandbox && s.PermissionMode == "" {
		return ErrHarnessChoiceNotManaged
	}
	return nil
}

// AdmitPermissionModeChange admits asking the Work's running Harness to
// change its permission mode: only the person who started a Managed Work,
// while its Harness is ready, to a mode a person may choose.
func (s *AgentWork) AdmitPermissionModeChange(requester string, mode PermissionMode) error {
	if s.PermissionMode == "" {
		return ErrHarnessChoiceNotManaged
	}
	if err := AdmitPermissionModeRequester(s.StartedBy, requester); err != nil {
		return err
	}
	if !mode.choosable() {
		return fmt.Errorf("%w: unknown permission mode", ErrInvalidWork)
	}
	return s.RequireReadyProcess()
}

// RecordPermissionMode retains the permission mode the Work's current Harness
// process reports it applies — after accepting a change, or after changing
// it itself, as when leaving plan mode — for the Work's later starts.
// sequence is the report's position in the process stream: an older or
// replayed report changes nothing, and the caller admits only reports about
// the current process (feedback ordering). A Cloud Work keeps none. The
// sandbox is never changed: it is chosen once, at the first start.
func (s *AgentWork) RecordPermissionMode(sequence int64, reported PermissionMode) error {
	if sequence <= 0 || !reported.known() {
		return fmt.Errorf("%w: unknown permission mode", ErrInvalidWork)
	}
	p := s.HarnessProcess
	if s.PermissionMode == "" || !p.Held() || sequence <= p.PermissionModeSequence {
		return nil
	}
	s.PermissionMode = reported
	p.PermissionModeSequence = sequence
	return nil
}
