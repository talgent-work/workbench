package sealing

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

func TestSealedVariablesOpenOnlyUnderTheirBindingAndKey(t *testing.T) {
	sealer, err := NewSealer(bytes.Repeat([]byte{7}, KeySize))
	require.NoError(t, err)
	variables := []domain.PersonalVariable{{Name: "TOKEN", Value: "secret-value"}, {Name: "PEM", Value: "a\nb"}}
	sealed, err := sealer.Seal(variables, Person("person-a"))
	require.NoError(t, err)
	require.NotContains(t, string(sealed), "secret-value")

	opened, err := sealer.Open(sealed, Person("person-a"))
	require.NoError(t, err)
	require.Equal(t, variables, opened)

	for _, binding := range []Binding{Person("person-b"), Execution("person-a")} {
		_, err = sealer.Open(sealed, binding)
		require.Error(t, err, "a sealed value never opens for another binding")
	}
	other, err := NewSealer(bytes.Repeat([]byte{8}, KeySize))
	require.NoError(t, err)
	_, err = other.Open(sealed, Person("person-a"))
	require.Error(t, err)
	_, err = sealer.Open(sealed[:4], Person("person-a"))
	require.Error(t, err)
	_, err = NewSealer([]byte("short"))
	require.Error(t, err)
}
