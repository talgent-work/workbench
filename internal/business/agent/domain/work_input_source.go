package domain

import (
	"fmt"
	"strings"
)

// WorkInputSource is the opaque read-only input reference supplied by the
// authorized creating caller, such as a File Space reference and Space Token
// the caller supplies for the Work it creates. The Work retains it from
// creation unchanged: Agent hands the reference to Storage for every start's
// mounts and passes the token into the sandbox for publication, never
// interpreting either. The zero value means none.
type WorkInputSource struct {
	Reference  string
	SpaceToken string
}

const (
	maxWorkInputSourceLength = 512
	maxSpaceTokenLength      = 64
)

func (s WorkInputSource) Present() bool { return s.Reference != "" }

// Validate accepts no source, or a reference together with its token.
func (s WorkInputSource) Validate() error {
	for _, value := range []string{s.Reference, s.SpaceToken} {
		if strings.TrimSpace(value) != value || strings.ContainsRune(value, 0) {
			return fmt.Errorf("%w: invalid Work input source", ErrInvalidWork)
		}
	}
	if len(s.Reference) > maxWorkInputSourceLength || len(s.SpaceToken) > maxSpaceTokenLength || (s.Reference == "") != (s.SpaceToken == "") {
		return fmt.Errorf("%w: invalid Work input source", ErrInvalidWork)
	}
	return nil
}
