package domain

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func personalVariables(t *testing.T) *PersonalVariableSet {
	t.Helper()
	set, err := NewPersonalVariableSet("person-a")
	require.NoError(t, err)
	return set
}

func TestPersonalVariableSetKeepsOneValuePerNameInNameOrder(t *testing.T) {
	set := personalVariables(t)
	for _, variable := range []PersonalVariable{{Name: "ZED", Value: "z"}, {Name: "API_KEY", Value: "one"}, {Name: "PEM", Value: "-----BEGIN-----\nline\n-----END-----\n"}} {
		changed, err := set.SetVariable("person-a", variable)
		require.NoError(t, err)
		require.True(t, changed)
	}
	changed, err := set.SetVariable("person-a", PersonalVariable{Name: "API_KEY", Value: "two"})
	require.NoError(t, err)
	require.True(t, changed)
	changed, err = set.SetVariable("person-a", PersonalVariable{Name: "API_KEY", Value: "two"})
	require.NoError(t, err)
	require.False(t, changed, "setting the held value changes nothing")

	names, err := set.Names("person-a")
	require.NoError(t, err)
	require.Equal(t, []string{"API_KEY", "PEM", "ZED"}, names)
	value, err := set.Reveal("person-a", "PEM")
	require.NoError(t, err)
	require.Equal(t, "-----BEGIN-----\nline\n-----END-----\n", value, "multi-line values are kept as they are")
	require.NoError(t, set.Validate())
}

func TestPersonalVariableSetRemovesANameAndIgnoresAnAbsentOne(t *testing.T) {
	set := personalVariables(t)
	_, err := set.SetVariable("person-a", PersonalVariable{Name: "TOKEN", Value: "v"})
	require.NoError(t, err)
	changed, err := set.RemoveVariable("person-a", "TOKEN")
	require.NoError(t, err)
	require.True(t, changed)
	changed, err = set.RemoveVariable("person-a", "TOKEN")
	require.NoError(t, err)
	require.False(t, changed)
	_, err = set.Reveal("person-a", "TOKEN")
	require.ErrorIs(t, err, ErrPersonalVariableNotFound)
	require.Empty(t, set.CurrentValues())
}

func TestPersonalVariableSetAdmitsOnlyItsPerson(t *testing.T) {
	set := personalVariables(t)
	_, err := set.SetVariable("person-a", PersonalVariable{Name: "TOKEN", Value: "v"})
	require.NoError(t, err)
	for _, requester := range []string{"person-b", ""} {
		_, err = set.SetVariable(requester, PersonalVariable{Name: "OTHER", Value: "v"})
		require.ErrorIs(t, err, ErrPersonalVariableForbidden)
		_, err = set.RemoveVariable(requester, "TOKEN")
		require.ErrorIs(t, err, ErrPersonalVariableForbidden)
		_, err = set.Names(requester)
		require.ErrorIs(t, err, ErrPersonalVariableForbidden)
		_, err = set.Reveal(requester, "TOKEN")
		require.ErrorIs(t, err, ErrPersonalVariableForbidden)
	}
	require.Equal(t, []PersonalVariable{{Name: "TOKEN", Value: "v"}}, set.CurrentValues())
}

func TestPersonalVariableSetRefusesNamesAndValuesAnEnvironmentCannotCarry(t *testing.T) {
	for _, tc := range []struct {
		variable PersonalVariable
		err      error
	}{
		{PersonalVariable{Name: "", Value: "v"}, ErrInvalidPersonalVariableName},
		{PersonalVariable{Name: "1ABC", Value: "v"}, ErrInvalidPersonalVariableName},
		{PersonalVariable{Name: "A-B", Value: "v"}, ErrInvalidPersonalVariableName},
		{PersonalVariable{Name: "A B", Value: "v"}, ErrInvalidPersonalVariableName},
		{PersonalVariable{Name: "TALGENT_WORK_ID", Value: "v"}, ErrReservedPersonalVariableName},
		{PersonalVariable{Name: "talgent_root", Value: "v"}, ErrReservedPersonalVariableName},
		{PersonalVariable{Name: "Talgent_X", Value: "v"}, ErrReservedPersonalVariableName},
		{PersonalVariable{Name: "VALUE", Value: "a\x00b"}, ErrInvalidPersonalVariableValue},
	} {
		set := personalVariables(t)
		_, err := set.SetVariable("person-a", tc.variable)
		require.ErrorIs(t, err, tc.err, tc.variable.Name)
		require.ErrorIs(t, err, ErrInvalidPersonalVariable)
		require.NotContains(t, err.Error(), "a\x00b", "a refusal never quotes the value")
		require.Empty(t, set.Variables)
	}
	set := personalVariables(t)
	for _, name := range []string{"_", "TALGENT", "TALGENTX", "MY_TALGENT_TOKEN", "a1"} {
		_, err := set.SetVariable("person-a", PersonalVariable{Name: name})
		require.NoError(t, err, name)
	}
}
