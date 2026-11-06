package nativeprocess

import (
	"slices"
	"strings"

	"github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
)

// subAgent is a native SubAgent task this process's main Agent delegated to.
type subAgent struct {
	ID     string `json:"id"`
	Type   string `json:"type,omitempty"`
	Task   string `json:"task,omitempty"`
	Status string `json:"status"`
}

const maxSubAgentTask = 512

// endStatuses normalizes the native end of a SubAgent task. Claude Code reports
// one stopped before finishing as stopped, and as killed in its task record.
var endStatuses = map[string]string{"completed": "completed", "failed": "failed", "killed": "stopped", "stopped": "stopped"}

// ObserveSubAgents folds native SubAgent task starts and ends into the
// complete snapshot of this process's main-Agent delegates. A SubAgent's own
// delegates are not the main Agent's: Claude Code names their parent task
// (later releases) and always gives them a spawn depth beyond one.
func (s *ProtocolState) ObserveSubAgents(message *Message) ([]command.SubAgent, bool) {
	if message.Type != "system" || message.TaskID == "" {
		return nil, false
	}
	index := slices.IndexFunc(s.SubAgents, func(agent subAgent) bool { return agent.ID == message.TaskID })
	switch message.Subtype {
	case "task_started":
		if message.TaskType != "local_agent" || message.ParentTaskID != "" || message.SpawnDepth > 1 {
			return nil, false
		}
		// Bounded: it is retained in the snapshot and on the Work.
		task := strings.ToValidUTF8(message.Description[:min(len(message.Description), maxSubAgentTask)], "")
		started := subAgent{ID: message.TaskID, Type: message.SubagentType, Task: task, Status: "running"}
		if index < 0 {
			s.SubAgents = append(s.SubAgents, started)
		} else {
			s.SubAgents[index] = started
		}
	case "task_notification":
		status, known := endStatuses[message.TaskStatus]
		if index < 0 || !known || s.SubAgents[index].Status == status {
			return nil, false
		}
		s.SubAgents[index].Status = status
	default:
		return nil, false
	}
	snapshot := make([]command.SubAgent, len(s.SubAgents))
	for i, agent := range s.SubAgents {
		snapshot[i] = command.SubAgent{ID: agent.ID, Type: agent.Type, Task: agent.Task, Status: agent.Status}
	}
	return snapshot, true
}
