package nativeprocess

import (
	"encoding/json"
	"slices"
	"time"

	"github.com/samber/oops"

	"github.com/talgent-work/workbench/internal/business/dispatcher/application"
	"github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
)

// ProtocolState is Dispatcher's fold over one Harness Output Stream, not
// AgentWork lifecycle. It lives in memory and is rebuilt by replay; a snapshot
// of it is kept only at boundaries. Offset is the next byte after a
// fully applied JSON line, so a replay starts at a line. Input and replies are
// not part of it: they live in the Work's input buffer until they settle, and
// Inputs and Replies only mirror that buffer for the reader.
type ProtocolState struct {
	Capabilities  application.Capabilities    `json:"capabilities"`
	Turn          application.TurnObservation `json:"turn"`
	PhaseSequence int64                       `json:"phase_sequence,omitempty"`
	CancelTurnID  string                      `json:"cancel_turn_id,omitempty"`
	CancelAck     bool                        `json:"cancel_ack,omitempty"`
	Projection    MessageProjection           `json:"projection"`
	PlanTasks     []planTask                  `json:"plan_tasks,omitempty"`
	PlanCalls     map[string]planCall         `json:"plan_calls,omitempty"`
	// FinishedTasks are native tasks of a batch that was completed before the
	// Harness created new ones; they stay in the native list but not in Work
	// Progress, which shows the current batch only.
	FinishedTasks []string `json:"finished_tasks,omitempty"`
	// SubAgents are this process's main-Agent delegates, in delegation order.
	// A later process of the same Session does not inherit them.
	SubAgents []subAgent `json:"subagents,omitempty"`
	// PermissionMode is the mode the process last reported it applies.
	PermissionMode string `json:"permission_mode,omitempty"`
	// Title is the title the process last reported for the conversation.
	Title  string `json:"title,omitempty"`
	Offset int64  `json:"offset"`
	Ready  bool   `json:"ready"`
	// Failure is a definitive native failure observed before exit, such as a
	// rejected initialize request. The exit fact carries it.
	Failure      string `json:"failure,omitempty"`
	Active       bool   `json:"active"`
	TurnObserved bool   `json:"turn_observed"`
	Exited       bool   `json:"exited"`
	ExitCode     int    `json:"exit_code"`
	// ExitAcknowledged means Agent applied the exit fact; until then it is
	// redelivered, including by whichever replica resumes the stream.
	ExitAcknowledged bool `json:"exit_acknowledged"`
	// DetachedSince is when Dispatcher lost its attachment to the process's
	// sandbox and has not attached again since. Kept in the snapshot, a loss
	// keeps its origin across attempts and replicas.
	// DetachmentReported records that Agent was told of that loss.
	DetachedSince      time.Time `json:"detached_since,omitzero"`
	DetachmentReported bool      `json:"detachment_reported,omitempty"`
	// Resumed records that a replica reads the process from this state and
	// has not given it up: an attachment it regained may be unrecorded, so a
	// replica restoring this state does not keep its loss.
	Resumed bool                `json:"resumed,omitempty"`
	Pending map[string]*Message `json:"pending"`
	// Inputs mirrors the buffered ordinary input by identity, Replies the
	// buffered replies by interaction.
	Inputs  map[string]command.BufferedInput `json:"-"`
	Replies map[string]command.BufferedInput `json:"-"`
}

// MirrorBuffer replaces the mirror of the Work's input buffer.
func (s *ProtocolState) MirrorBuffer(entries []command.BufferedInput) {
	s.Inputs, s.Replies = map[string]command.BufferedInput{}, map[string]command.BufferedInput{}
	for _, entry := range entries {
		if entry.Reply != nil {
			s.Replies[entry.ID] = entry
		} else {
			s.Inputs[entry.ID] = entry
		}
	}
}

// ForgetBuffered drops a settled entry from the mirror.
func (s *ProtocolState) ForgetBuffered(entry command.BufferedInput) {
	if entry.Reply != nil {
		delete(s.Replies, entry.ID)
	} else {
		delete(s.Inputs, entry.ID)
	}
}

// PendingInteractions are the Harness requests awaiting a reply. Whether one
// already has a buffered reply is read from the buffer by whoever applies them
// (Pending input authority).
func (s *ProtocolState) PendingInteractions() []string {
	ids := make([]string, 0, len(s.Pending))
	for id := range s.Pending {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

// ResumeReading marks a state with a recorded loss as read by a replica
// again, and reports whether it must be stored so before reading starts: an
// attachment regained afterwards may never be recorded.
func (s *ProtocolState) ResumeReading() bool {
	if s.DetachedSince.IsZero() {
		return false
	}
	s.Resumed = true
	return true
}

// GiveUpReading marks a state no replica reads any more, so whatever loss it
// records stands for the replica that resumes it.
func (s *ProtocolState) GiveUpReading() { s.Resumed = false }

func NewProtocolState() *ProtocolState {
	return &ProtocolState{Pending: map[string]*Message{}, Inputs: map[string]command.BufferedInput{}, Replies: map[string]command.BufferedInput{}}
}

// snapshot is the stored form of a Work stream's state at a boundary.
type snapshot struct {
	ProcessID       string        `json:"process_id"`
	NativeSessionID string        `json:"native_session_id"`
	State           ProtocolState `json:"state"`
}

// EncodeSnapshot records the state of the process the binding names.
func EncodeSnapshot(binding command.ConnectExecution, state *ProtocolState) ([]byte, error) {
	raw, err := json.Marshal(snapshot{ProcessID: binding.ProcessID, NativeSessionID: binding.NativeSessionID, State: *state})
	return raw, oops.Wrap(err)
}

// RestoreState rebuilds the state to resume reading the binding's process
// from. A snapshot of the same process resumes at its offset. A snapshot of an
// earlier process of the same Harness Session carries only what refers to the
// Session: completed tasks and message attribution, never interactions or
// transport state. Otherwise reading starts at offset 0.
func RestoreState(binding command.ConnectExecution, raw []byte) (*ProtocolState, error) {
	state := NewProtocolState()
	if len(raw) == 0 {
		return state, nil
	}
	var stored snapshot
	if err := json.Unmarshal(raw, &stored); err != nil {
		return nil, oops.With("agent_work_id", binding.WorkID).Wrap(err)
	}
	switch {
	case stored.ProcessID == binding.ProcessID && stored.State.Offset >= 0:
		state = &stored.State
		if state.Pending == nil {
			state.Pending = map[string]*Message{}
		}
		state.Inputs, state.Replies = map[string]command.BufferedInput{}, map[string]command.BufferedInput{}
		if state.Resumed {
			// The replica that resumed it never gave it up: whether it
			// attached again is unknown, so the loss is known only from the
			// next failed attempt.
			state.DetachedSince, state.DetachmentReported, state.Resumed = time.Time{}, false, false
		}
	case stored.NativeSessionID == binding.NativeSessionID:
		prior := stored.State
		state.PlanTasks, state.FinishedTasks = slices.Clone(prior.PlanTasks), slices.Clone(prior.FinishedTasks)
		state.Projection = MessageProjection{InputTurns: prior.Projection.InputTurns, Tools: prior.Projection.Tools, Agents: prior.Projection.Agents, Requests: prior.Projection.Requests}
	}
	return state, nil
}
