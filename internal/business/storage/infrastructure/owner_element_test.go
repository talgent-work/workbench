package infrastructure

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

func TestOwnerElementIsOnePathElementOfItsFactsInOrder(t *testing.T) {
	facts := []domain.OwnerFact{{Condition: "project", Value: "p1"}, {Condition: "subject", Value: "case:1"}}
	require.Equal(t, "project=p1,subject=case:1", ownerElement(facts))
	parsed, err := parseOwnerElement(ownerElement(facts))
	require.NoError(t, err)
	require.Equal(t, facts, parsed)
}

func TestOwnerElementEscapesEveryOtherByte(t *testing.T) {
	facts := []domain.OwnerFact{{Condition: "person", Value: "a/b,c=d %é"}}
	element := ownerElement(facts)
	require.Equal(t, "person=a%2Fb%2Cc%3Dd%20%25%C3%A9", element)
	require.NotContains(t, element, "/")
	parsed, err := parseOwnerElement(element)
	require.NoError(t, err)
	require.Equal(t, facts, parsed)
}

func TestParseOwnerElementRefusesWhatOwnerElementNeverWrites(t *testing.T) {
	for _, element := range []string{"", "subject", "subject=a%2f", "subject=a%2", "subject=a b", "subject=a%41"} {
		_, err := parseOwnerElement(element)
		require.ErrorIs(t, err, errInvalidOwnerElement, element)
	}
}
