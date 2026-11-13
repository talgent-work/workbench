package standalonework

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A person asks for themselves; an Agent asks from its Work and owns
// nothing, so it acts for no owner and has nothing only a person has.
func TestWhomACallerActsFor(t *testing.T) {
	person := Caller{UserID: "u1"}
	require.Equal(t, "u1", person.self())
	require.Equal(t, "u1", person.Owner())

	agent := Caller{Agent: &ActingAgent{WorkID: "w1"}}
	require.Empty(t, agent.self())
	require.Empty(t, agent.Owner())
	require.Zero(t, agent.fileActor())
}

// An Application owns its Standalone Works but has nothing only a person
// has.
func TestAnApplicationAsksOnlyAsAnOwner(t *testing.T) {
	application := Caller{ApplicationID: "a1"}
	require.Equal(t, "a1", application.Owner())
	require.Empty(t, application.self())
}
