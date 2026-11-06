package infrastructure

func runtimeMessageSubject(sessionID string) string {
	return "session." + sessionID
}

func workStateSubject(sessionID string) string {
	return "session_state." + sessionID
}

func diagnosticSubject(sessionID string) string {
	return "session." + sessionID + ".diagnostic"
}

func workAccessSubject(sessionID string) string {
	return "session_access." + sessionID
}
