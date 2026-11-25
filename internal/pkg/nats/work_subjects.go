package nats

import "strings"

// A Work's live frames travel on work.<id>.<kind>, so StreamGateway reads all
// of one Work's frames through a single work.<id>.> subscription. Dispatcher
// publishes messages and diagnostics, Agent the state and access frames.
const (
	WorkMessageFrames    = "message"
	WorkDiagnosticFrames = "diagnostic"
	WorkStateFrames      = "state"
	WorkAccessFrames     = "access"
)

const workSubjectPrefix = "work."

// WorkSubject is the subject of one kind of a Work's live frames.
func WorkSubject(workID, kind string) string {
	return workSubjectPrefix + workID + "." + kind
}

// WorkFrames is the subject that matches every live frame of a Work.
func WorkFrames(workID string) string {
	return workSubjectPrefix + workID + ".>"
}

// WorkFrameKind is the kind a subject of WorkFrames(workID) names.
func WorkFrameKind(workID, subject string) (string, bool) {
	return strings.CutPrefix(subject, workSubjectPrefix+workID+".")
}

// ValidWorkID reports whether workID is one subject token, so a reader's Work
// identity can never widen a subscription to other Works' frames.
func ValidWorkID(workID string) bool {
	return workID != "" && !strings.ContainsAny(workID, ".*> \t\r\n")
}
