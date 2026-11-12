package command

import (
	"errors"

	"github.com/google/uuid"
)

// ErrInvalidStartRequest refuses a request identity that is not a UUID.
var ErrInvalidStartRequest = errors.New("agent: invalid start request")

// A caller's request identity names everything its operation takes, so a
// repeat after a lost answer names the same Work and asks its Computer for the
// same Desk; Computer admits each Desk once and refuses one that left. No
// request record is kept (Caller-owned request identity, Unique start
// request).
var (
	requestWorkNamespace = uuid.MustParse("2f8d4c1e-5b7a-4e0f-9c3d-6a1b8e2f7d40")
	requestDeskNamespace = uuid.MustParse("9b3e7a2c-1d4f-4a8b-b6e5-0c7d2f9a1e83")
)

// requestedWorkID is the identity of the Work a creation request establishes.
func requestedWorkID(requestID string) string {
	return uuid.NewSHA1(requestWorkNamespace, []byte(requestID)).String()
}

// requestedDeskID is the Desk a creation, Resume or Restart request asks its
// Work's Computer for. A request identity is its caller's own, so the Desk is
// scoped to whom the Work acts as: equal request identities of two owners
// never name the same Desk, while one owner reusing its request identity for
// another Work meets the Desk it already took.
func requestedDeskID(workPrincipal, requestID string) string {
	return uuid.NewSHA1(requestDeskNamespace, []byte(workPrincipal+"/"+requestID)).String()
}
