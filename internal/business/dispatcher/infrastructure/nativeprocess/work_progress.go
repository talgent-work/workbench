package nativeprocess

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
)

// These are native protocol checkpoints, not another queryable Work projection.
// They normalize task deltas into complete snapshots for the Work owner.
type planTask struct {
	ID         string `json:"id"`
	Subject    string `json:"subject"`
	Status     string `json:"status"`
	ActiveForm string `json:"activeForm,omitempty"`
}

type planCall struct {
	Name  string    `json:"name"`
	Input planInput `json:"input"`
}

type planInput struct {
	TaskID     string  `json:"taskId"`
	Subject    string  `json:"subject"`
	ActiveForm *string `json:"activeForm"`
}

type planTodo struct {
	Content    string `json:"content"`
	Status     string `json:"status"`
	ActiveForm string `json:"activeForm"`
}

func (s *ProtocolState) ObserveWorkProgress(message *Message) ([]command.WorkProgressItem, bool) {
	if message.ParentToolUseID != "" || message.AgentID != "" || (message.Type != "assistant" && message.Type != "user") {
		return nil, false
	}
	var blocks []struct {
		Type, ID, Name string
		Input          planInput
		ToolUseID      string `json:"tool_use_id"`
		IsError        bool   `json:"is_error"`
	}
	if json.Unmarshal(message.Message.Content, &blocks) != nil {
		return nil, false
	}
	updated := false
	for _, block := range blocks {
		if message.Type == "assistant" && block.Type == "tool_use" && block.ID != "" {
			name := strings.ToLower(block.Name)
			switch name {
			case "todowrite", "todowriter", "taskcreate", "taskupdate", "taskget", "tasklist":
				if s.PlanCalls == nil {
					s.PlanCalls = make(map[string]planCall)
				}
				s.PlanCalls[block.ID] = planCall{Name: name, Input: block.Input}
			}
			continue
		}
		if message.Type != "user" || block.Type != "tool_result" {
			continue
		}
		call, found := s.PlanCalls[block.ToolUseID]
		delete(s.PlanCalls, block.ToolUseID)
		if !found || block.IsError {
			continue
		}
		if s.applyPlanResult(call, message.ToolUseResult) {
			updated = true
		}
	}
	if !updated {
		return nil, false
	}
	current := s.currentTasks()
	items := make([]command.WorkProgressItem, len(current))
	for i, task := range current {
		items[i] = command.WorkProgressItem{Content: task.Subject, Status: task.Status, ActiveForm: task.ActiveForm}
	}
	return items, true
}

// currentTasks is the batch Work Progress shows: the native tasks created
// since every earlier task was completed.
func (s *ProtocolState) currentTasks() []planTask {
	return slices.DeleteFunc(slices.Clone(s.PlanTasks), func(task planTask) bool { return slices.Contains(s.FinishedTasks, task.ID) })
}

// beginTasks finishes the current batch when every task in it is completed,
// so the new task starts the next batch instead of extending the last one.
func (s *ProtocolState) beginTasks() {
	current := s.currentTasks()
	if len(current) == 0 || slices.ContainsFunc(current, func(task planTask) bool { return task.Status != "completed" }) {
		return
	}
	for _, task := range current {
		s.FinishedTasks = append(s.FinishedTasks, task.ID)
	}
}

func (s *ProtocolState) applyPlanResult(call planCall, raw json.RawMessage) bool {
	var result struct {
		Task          *planTask  `json:"task"`
		Tasks         []planTask `json:"tasks"`
		Success       bool       `json:"success"`
		TaskID        string     `json:"taskId"`
		NewTodos      []planTodo `json:"newTodos"`
		UpdatedFields []string   `json:"updatedFields"`
		StatusChange  struct {
			To string `json:"to"`
		} `json:"statusChange"`
	}
	if json.Unmarshal(raw, &result) != nil {
		return false
	}
	if call.Name == "todowrite" || call.Name == "todowriter" {
		if result.NewTodos == nil {
			return false
		}
		tasks := make([]planTask, len(result.NewTodos))
		for i, todo := range result.NewTodos {
			tasks[i] = planTask{Subject: todo.Content, Status: todo.Status, ActiveForm: todo.ActiveForm}
			if !validPlanTask(tasks[i]) {
				return false
			}
		}
		// A todo list is complete on every write; it never keeps a past batch.
		s.PlanTasks, s.FinishedTasks = tasks, nil
		return true
	}
	index := func(id string) int {
		return slices.IndexFunc(s.PlanTasks, func(task planTask) bool { return task.ID == id })
	}
	switch call.Name {
	case "taskcreate", "taskget":
		if result.Task == nil || result.Task.ID == "" {
			return false
		}
		task := *result.Task
		if call.Name == "taskcreate" && task.Status == "" {
			task.Status = "pending"
		}
		if !validPlanTask(task) {
			return false
		}
		i := index(task.ID)
		if i >= 0 && task.ActiveForm == "" {
			task.ActiveForm = s.PlanTasks[i].ActiveForm
		}
		if call.Input.ActiveForm != nil {
			task.ActiveForm = *call.Input.ActiveForm
		}
		if i < 0 {
			if call.Name == "taskcreate" {
				s.beginTasks()
			}
			s.PlanTasks = append(s.PlanTasks, task)
		} else {
			s.PlanTasks[i] = task
		}
	case "taskupdate":
		if !result.Success || result.TaskID != call.Input.TaskID || result.TaskID == "" {
			return false
		}
		i := index(result.TaskID)
		if i < 0 {
			return false
		}
		if slices.Contains(result.UpdatedFields, "deleted") {
			s.PlanTasks = slices.Delete(s.PlanTasks, i, i+1)
			s.FinishedTasks = slices.DeleteFunc(s.FinishedTasks, func(id string) bool { return id == result.TaskID })
			return true
		}
		task := s.PlanTasks[i]
		if slices.Contains(result.UpdatedFields, "subject") {
			task.Subject = call.Input.Subject
		}
		if slices.Contains(result.UpdatedFields, "status") {
			task.Status = result.StatusChange.To
		}
		if slices.Contains(result.UpdatedFields, "activeForm") && call.Input.ActiveForm != nil {
			task.ActiveForm = *call.Input.ActiveForm
		}
		if !validPlanTask(task) {
			return false
		}
		s.PlanTasks[i] = task
	case "tasklist":
		if result.Tasks == nil {
			return false
		}
		for i := range result.Tasks {
			task := &result.Tasks[i]
			if task.ID == "" || !validPlanTask(*task) {
				return false
			}
			if old := index(task.ID); old >= 0 && task.ActiveForm == "" {
				task.ActiveForm = s.PlanTasks[old].ActiveForm
			}
		}
		s.PlanTasks = result.Tasks
		s.FinishedTasks = slices.DeleteFunc(s.FinishedTasks, func(id string) bool { return index(id) < 0 })
	default:
		return false
	}
	return true
}

func validPlanTask(task planTask) bool {
	return strings.TrimSpace(task.Subject) != "" && (task.Status == "pending" || task.Status == "in_progress" || task.Status == "completed")
}
