//go:build integration

package account_test

import (
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/stretchr/testify/require"

	accountpublicv1 "github.com/talgent-work/contracts/gen/go/account/public/v1"
	"github.com/talgent-work/workbench/internal/business/account/domain"
)

func TestRemovalRevokesEverySecretAtOnceAndPublishesIt(t *testing.T) {
	h := newHarness(t)
	service := h.applicationService()
	owner := newPerson()
	registered, err := service.RegisterApplication(as("user", owner), &accountpublicv1.RegisterApplicationRequest{Name: "Support Desk"})
	require.NoError(t, err)
	id := registered.GetApplication().GetId()
	rotated, err := service.RotateClientSecret(as("user", owner), &accountpublicv1.RotateClientSecretRequest{ApplicationId: id})
	require.NoError(t, err)
	status, body := h.grant(id, rotated.GetClientSecret())
	require.Equal(t, http.StatusOK, status)
	issued := h.verify(body["access_token"].(string))

	_, err = service.RemoveApplication(as("user", owner), &accountpublicv1.RemoveApplicationRequest{ApplicationId: id})
	require.NoError(t, err)

	for _, secret := range []string{registered.GetClientSecret(), rotated.GetClientSecret()} {
		status, body := h.grant(id, secret)
		require.Equal(t, http.StatusUnauthorized, status, "the grant refuses the current and the overlapping secret at once")
		require.Equal(t, "invalid_client", body["error"])
	}
	expires, ok := issued.Expiration()
	require.True(t, ok)
	require.WithinDuration(t, h.clock.Now().Add(applicationTokenLifetime), expires, time.Second,
		"a token already issued is not recalled: it stops at its short expiry")

	_, err = service.GetApplication(as("user", owner), &accountpublicv1.GetApplicationRequest{ApplicationId: id})
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	listed, err := service.ListOwnedApplications(as("user", owner), &accountpublicv1.ListOwnedApplicationsRequest{})
	require.NoError(t, err)
	require.Empty(t, listed.GetApplications())

	require.Equal(t, []domain.ApplicationRemovedEvent{{ApplicationID: domain.ApplicationID(id), At: h.clock.Now()}}, h.removals.published())
}

func TestRemovalIsFinalAndTheOwnersAlone(t *testing.T) {
	h := newHarness(t)
	service := h.applicationService()
	owner := newPerson()
	registered, err := service.RegisterApplication(as("user", owner), &accountpublicv1.RegisterApplicationRequest{Name: "Support Desk"})
	require.NoError(t, err)
	id := registered.GetApplication().GetId()

	_, err = service.RemoveApplication(as("user", newPerson()), &accountpublicv1.RemoveApplicationRequest{ApplicationId: id})
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err), "another person learns nothing about it")
	status, _ := h.grant(id, registered.GetClientSecret())
	require.Equal(t, http.StatusOK, status)
	require.Empty(t, h.removals.published())

	_, err = service.RemoveApplication(as("application", id), &accountpublicv1.RemoveApplicationRequest{ApplicationId: id})
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err), "an Application never removes itself")

	_, err = service.RemoveApplication(as("user", owner), &accountpublicv1.RemoveApplicationRequest{ApplicationId: id})
	require.NoError(t, err)
	_, err = service.RemoveApplication(as("user", owner), &accountpublicv1.RemoveApplicationRequest{ApplicationId: id})
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err), "a removed Application never returns")
	require.Len(t, h.removals.published(), 1)
}

func TestRemovalStandsWhenItsPublicationFails(t *testing.T) {
	h := newHarness(t)
	h.removals.fail = true
	service := h.applicationService()
	owner := newPerson()
	registered, err := service.RegisterApplication(as("user", owner), &accountpublicv1.RegisterApplicationRequest{Name: "Support Desk"})
	require.NoError(t, err)
	id := registered.GetApplication().GetId()

	_, err = service.RemoveApplication(as("user", owner), &accountpublicv1.RemoveApplicationRequest{ApplicationId: id})
	require.NoError(t, err, "the removal is committed; the fact is best effort")
	status, _ := h.grant(id, registered.GetClientSecret())
	require.Equal(t, http.StatusUnauthorized, status)
	require.Len(t, h.removals.published(), 1)
}
