//go:build integration

package account_test

import (
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/stretchr/testify/require"

	accountpublicv1 "github.com/talgent-work/contracts/gen/go/account/public/v1"
)

// The Application Owner renames the Application, and only they do: another
// person learns nothing of it, and a name outside 1 to 60 characters is
// refused.
func TestTheOwnerRenamesTheApplication(t *testing.T) {
	h := newHarness(t)
	service := h.applicationService()
	owner := newPerson()
	registered, err := service.RegisterApplication(as("user", owner), &accountpublicv1.RegisterApplicationRequest{Name: "Support Desk"})
	require.NoError(t, err)
	id := registered.GetApplication().GetId()

	renamed, err := service.RenameApplication(as("user", owner), &accountpublicv1.RenameApplicationRequest{ApplicationId: id, Name: "  Help Center  "})
	require.NoError(t, err)
	require.Equal(t, "Help Center", renamed.GetApplication().GetName())
	read, err := service.GetApplication(as("user", owner), &accountpublicv1.GetApplicationRequest{ApplicationId: id})
	require.NoError(t, err)
	require.Equal(t, "Help Center", read.GetApplication().GetName())

	_, err = service.RenameApplication(as("user", newPerson()), &accountpublicv1.RenameApplicationRequest{ApplicationId: id, Name: "Mine"})
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	for _, name := range []string{" ", strings.Repeat("a", 61)} {
		_, err = service.RenameApplication(as("user", owner), &accountpublicv1.RenameApplicationRequest{ApplicationId: id, Name: name})
		require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	}
	read, err = service.GetApplication(as("user", owner), &accountpublicv1.GetApplicationRequest{ApplicationId: id})
	require.NoError(t, err)
	require.Equal(t, "Help Center", read.GetApplication().GetName())
}
