package nativeprocess

import "slices"

// PermissionModes are the permission modes the Harness applies and reports,
// as Claude Code names them; the platform offers a person only some of them.
var PermissionModes = []string{"default", "acceptEdits", "plan", "bypassPermissions", "dontAsk", "auto"}

// ReportedPermissionMode is the permission mode a system message says the
// Harness applies: its init message at each Turn, and the status message it
// writes whenever the mode changes, also by itself, as when it leaves plan
// mode once its plan was approved. Empty for any other message.
func ReportedPermissionMode(message *Message) string {
	if message.Type != "system" || (message.Subtype != "init" && message.Subtype != "status") {
		return ""
	}
	return message.PermissionMode
}

// ObservePermissionMode folds a mode the Harness reports into this process's
// last known one. It reports the mode when it changed; a mode outside
// PermissionModes, from a later Harness, changes nothing and is reported as
// unknown, so the Work keeps the last mode it knows.
func (s *ProtocolState) ObservePermissionMode(mode string) (changed, known bool) {
	if !slices.Contains(PermissionModes, mode) {
		return false, false
	}
	if s.PermissionMode == mode {
		return false, true
	}
	s.PermissionMode = mode
	return true, true
}
