// Package personalwork is a person's own use of their Agent: the Personal
// Works they start for themselves, their files and sharing, and the
// person's Personal Library. Agent decides every access itself before calling
// Storage, which keeps the files.
package personalwork

import "errors"

var (
	ErrInvalidRequest = errors.New("agent: invalid personal work request")
	// ErrReaderUnknown means the User Directory has no such person.
	ErrReaderUnknown = errors.New("agent: the reader is not in the User Directory")
)
