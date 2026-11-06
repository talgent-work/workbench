package connectrpc

import (
	"crypto/subtle"
	"errors"
	"strings"

	"connectrpc.com/connect"
)

// controlToken admits the trusted platform services calling Computer's
// private contracts: Agent and the Computer Gateway.
type controlToken string

func (t controlToken) authenticate(header string) error {
	if strings.TrimSpace(string(t)) == "" || subtle.ConstantTimeCompare([]byte(header), []byte("Bearer "+string(t))) != 1 {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("computer: control authentication required"))
	}
	return nil
}
