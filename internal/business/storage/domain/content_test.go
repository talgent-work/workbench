package domain

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestContentExpectationAdmitsOnlyTheContentItNames(t *testing.T) {
	require.NoError(t, AnyContent().Admit(""))
	require.NoError(t, AnyContent().Admit("r1"))

	require.NoError(t, NoContent().Admit(""))
	require.ErrorIs(t, NoContent().Admit("r1"), ErrContentChanged)

	expected, err := ExpectRevision("r1")
	require.NoError(t, err)
	require.NoError(t, expected.Admit("r1"))
	require.ErrorIs(t, expected.Admit("r2"), ErrContentChanged)
	require.ErrorIs(t, expected.Admit(""), ErrContentChanged)

	_, err = ExpectRevision("")
	require.ErrorIs(t, err, ErrInvalidContentRevision)
}
