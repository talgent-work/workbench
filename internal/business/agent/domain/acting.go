package domain

import "errors"

// ErrWorkNotActing refuses an action of a Work that does not act now.
var ErrWorkNotActing = errors.New("agent: the Work is not acting")

// AdmitAgentAction admits an action of the Work's own Agent only while the
// Work is Working and awaits no required response from its person (Acting
// Work). A reply delivered is not a response resolved, so a Work still
// awaiting one does not act; waiting briefly for that resolution is its
// caller's (Reply resolution wait).
func (s *AgentWork) AdmitAgentAction(awaitsRequiredResponse bool) error {
	if s.State != WorkWorking || awaitsRequiredResponse {
		return ErrWorkNotActing
	}
	return nil
}
