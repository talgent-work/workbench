package tokenmaintenance

import "strings"

type RefreshAuthorizationPayload struct {
	AuthorizationID string `json:"authorization_id"`
	Attempt         int    `json:"attempt,omitempty"`
	DeadlineUnixMS  int64  `json:"deadline_unix_ms,omitempty"`
	Reason          string `json:"reason,omitempty"`
}

func (p RefreshAuthorizationPayload) Validate() error {
	if strings.TrimSpace(p.AuthorizationID) == "" {
		return ErrInvalidPayload
	}
	return nil
}
