package adapter

// Notice is told live to people reading a Work. It is not part of the Work
// Message Stream and is never stored.
type Notice struct {
	Reason  string
	Message string
}

// Notice reasons, told live with severity "warning"; the web client mirrors
// them: model provider refusals the Harness or a SubAgent recovers from, and a
// reply whose request had already closed when it was delivered.
const (
	NoticeModelRateLimited    = "model_rate_limited"
	NoticeModelUnavailable    = "model_unavailable"
	NoticeInteractionClosed   = "interaction_closed"
	interactionClosedSentence = "The request you answered had already closed, so your answer was not used."
)

// InteractionClosed tells readers a reply arrived after its request closed.
func InteractionClosed() *Notice {
	return &Notice{Reason: NoticeInteractionClosed, Message: interactionClosedSentence}
}
