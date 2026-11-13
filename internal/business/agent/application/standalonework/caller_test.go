package standalonework

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A person reads their own Library; the Agent of their Work without a
// Project reads it for them; the Agent of a Work with a Project, or of another purpose, reads
// no one's. An Agent asks nothing else for anyone.
func TestWhomALibraryReadIsFor(t *testing.T) {
	person := Caller{UserID: "u1"}
	require.Equal(t, "u1", person.libraryReader())
	require.Equal(t, "u1", person.self())
	require.Equal(t, "u1", person.Owner())

	personal := Caller{UserID: "u1", Agent: &ActingAgent{Purpose: "work"}}
	require.Equal(t, "u1", personal.libraryReader())
	require.Empty(t, personal.self())
	require.Empty(t, personal.Owner())

	for _, agent := range []*ActingAgent{{ProjectID: "p1", Purpose: "work"}, {Purpose: "project_coordination"}} {
		require.Empty(t, Caller{UserID: "u1", Agent: agent}.libraryReader())
	}
}

// An Application owns its Standalone Works but has no Personal Library and
// nothing only a person has.
func TestAnApplicationAsksOnlyAsAnOwner(t *testing.T) {
	application := Caller{ApplicationID: "a1"}
	require.Equal(t, "a1", application.Owner())
	require.Empty(t, application.self())
	require.Empty(t, application.libraryReader())
}
