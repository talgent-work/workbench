package personalwork

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A person reads their own Library; the Agent of their Work without a
// Project reads it for them; the Agent of a Work with a Project, or of another purpose, reads
// no one's. An Agent asks nothing else for anyone.
func TestWhomALibraryReadIsFor(t *testing.T) {
	person := Person{UserID: "u1"}
	require.Equal(t, "u1", person.libraryReader())
	require.Equal(t, "u1", person.self())

	personal := Person{UserID: "u1", Agent: &ActingAgent{Purpose: "work"}}
	require.Equal(t, "u1", personal.libraryReader())
	require.Empty(t, personal.self())

	for _, agent := range []*ActingAgent{{ProjectID: "p1", Purpose: "work"}, {Purpose: "project_coordination"}} {
		require.Empty(t, Person{UserID: "u1", Agent: agent}.libraryReader())
	}
}
