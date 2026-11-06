package domain

import "errors"

// ErrWorkNotActing refuses an action of a Work that does not act now.
var ErrWorkNotActing = errors.New("agent: the Work is not acting")

// Acts reports whether the Work may act for its person: it is Working and does
// not await its person's required response. A reply delivered is not a
// response resolved, so a Work still awaiting one does not act.
func (s *AgentWork) Acts(awaitsRequiredResponse bool) bool {
	return s.State == WorkWorking && !awaitsRequiredResponse
}
