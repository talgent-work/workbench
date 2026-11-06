package access

import (
	"context"
	"errors"
)

var ErrAccessDenied = errors.New("agent: access denied")

// WorkAuthorizer checks the caller's authority for one conversation.
// Authentication and reference selection remain at the inbound boundary.
type WorkAuthorizer interface {
	AuthorizeWork(context.Context, string, string) error
}
