package storagepath

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOwnerIsOnePathElementOfItsFactsInOrder(t *testing.T) {
	facts := []OwnerFact{{Condition: "project", Value: "p1"}, {Condition: "subject", Value: "case:1"}}
	require.Equal(t, "project=p1,subject=case:1", Owner(facts))
	require.Equal(t, "project=p1,subject=case:1/inputs", Directory(facts, "inputs"))

	parsed, err := ParseOwner(Owner(facts))
	require.NoError(t, err)
	require.Equal(t, facts, parsed)
}

func TestOwnerEscapesEveryOtherByte(t *testing.T) {
	facts := []OwnerFact{{Condition: "person", Value: "a/b,c=d %é"}}
	element := Owner(facts)
	require.Equal(t, "person=a%2Fb%2Cc%3Dd%20%25%C3%A9", element)
	require.NotContains(t, element, "/")

	parsed, err := ParseOwner(element)
	require.NoError(t, err)
	require.Equal(t, facts, parsed)
}

func TestParseOwnerRefusesWhatOwnerNeverWrites(t *testing.T) {
	for _, element := range []string{"", "subject", "subject=a%2f", "subject=a%2", "subject=a b", "subject=a%41"} {
		_, err := ParseOwner(element)
		require.ErrorIs(t, err, ErrInvalidOwner, element)
	}
}
