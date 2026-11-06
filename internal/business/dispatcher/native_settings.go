package dispatcher

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/talgent-work/workbench/internal/business/dispatcher/application"
	"github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/nativeprocess"
)

// Settings replies are private control traffic, including when replayed after
// their original waiter has gone away. Never project the native settings body.
const (
	thinkingRequestPrefix   = "talgent-thinking-"
	permissionRequestPrefix = "talgent-permission-"
)

// reportedPermissionMode is the permission mode a message says the Harness
// now applies: a system init or status message, or the Harness's answer
// accepting a set_permission_mode request.
func reportedPermissionMode(message *nativeprocess.Message) string {
	if message.Type == "control_response" && message.Response.Subtype == "success" && strings.HasPrefix(message.Response.RequestID, permissionRequestPrefix) {
		return message.Response.Response.Mode
	}
	return nativeprocess.ReportedPermissionMode(message)
}

// privateSetting reports a control response to one of the settings requests
// above.
func privateSetting(requestID string) bool {
	return strings.HasPrefix(requestID, thinkingRequestPrefix) || strings.HasPrefix(requestID, permissionRequestPrefix)
}

func (m *NativeExecutions) SetThinkingLevel(ctx context.Context, input application.SetThinkingLevel) error {
	// Native apply_flag_settings also acknowledges arbitrary strings. Restrict
	// this protocol surface; Agent validates the deployed model's capability.
	switch input.Level {
	case "", "low", "medium", "high", "xhigh", "max":
	default:
		return command.ErrExecutionConnection
	}
	_, err := m.setting(ctx, input.WorkID, thinkingRequestPrefix, func(connection *nativeprocess.Connection, id string) error {
		return connection.SetThinkingLevel(ctx, id, input.Level)
	})
	return err
}

func (m *NativeExecutions) GetThinkingLevel(ctx context.Context, workID string) (string, error) {
	return m.setting(ctx, workID, thinkingRequestPrefix, func(connection *nativeprocess.Connection, id string) error {
		return connection.GetThinkingLevel(ctx, id)
	})
}

// SetPermissionMode asks the Harness to change its permission mode at once
// and answers the mode it accepted, which also reaches Agent as feedback of
// this process. A mode the Harness refuses, such as bypassing permission
// prompts in a process not launched to allow it, fails.
func (m *NativeExecutions) SetPermissionMode(ctx context.Context, input application.SetPermissionMode) (string, error) {
	if !slices.Contains(nativeprocess.PermissionModes, input.Mode) {
		return "", command.ErrExecutionConnection
	}
	return m.setting(ctx, input.WorkID, permissionRequestPrefix, func(connection *nativeprocess.Connection, id string) error {
		return connection.SetPermissionMode(ctx, id, input.Mode)
	})
}

// settingResult is the Harness's answer to one settings request.
type settingResult struct {
	value string
	err   error
}

// setting sends one settings request to the Work's ready Harness and waits
// for its private control response.
func (m *NativeExecutions) setting(ctx context.Context, workID, prefix string, send func(*nativeprocess.Connection, string) error) (string, error) {
	entry, err := m.find(workID)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	entry.mu.Lock()
	if entry.ProtocolState == nil || entry.broken || !entry.Ready || !entry.attached || entry.Exited {
		entry.mu.Unlock()
		return "", command.ErrExecutionNotReady
	}
	id := prefix + uuid.Must(uuid.NewV7()).String()
	response := make(chan settingResult, 1)
	if entry.settings == nil {
		entry.settings = make(map[string]chan settingResult)
	}
	entry.settings[id] = response
	err = send(entry.connection, id)
	entry.mu.Unlock()
	defer func() {
		entry.mu.Lock()
		delete(entry.settings, id)
		entry.mu.Unlock()
	}()
	if err != nil {
		return "", err
	}
	select {
	case result := <-response:
		return result.value, result.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}
