//go:build integration

package account_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/stretchr/testify/require"

	accountpublicv1 "github.com/talgent-work/contracts/gen/go/account/public/v1"
)

func TestRotatedSecretKeepsWorkingForTheOverlapThenStops(t *testing.T) {
	h := newHarness(t)
	service := h.applicationService()
	owner := newPerson()
	registered, err := service.RegisterApplication(as("user", owner), &accountpublicv1.RegisterApplicationRequest{Name: "Support Desk"})
	require.NoError(t, err)
	id := registered.GetApplication().GetId()
	first := registered.GetClientSecret()

	rotated, err := service.RotateClientSecret(as("user", owner), &accountpublicv1.RotateClientSecretRequest{ApplicationId: id})
	require.NoError(t, err)
	second := rotated.GetClientSecret()
	require.True(t, strings.HasPrefix(second, "wbs_"))
	require.NotEqual(t, first, second)
	secrets := rotated.GetApplication().GetClientSecrets()
	require.Len(t, secrets, 2)
	require.Equal(t, accountpublicv1.ClientSecretState_CLIENT_SECRET_STATE_CURRENT, secrets[0].GetState())
	require.Equal(t, second[len(second)-4:], secrets[0].GetHint())
	require.Equal(t, accountpublicv1.ClientSecretState_CLIENT_SECRET_STATE_OVERLAPPING, secrets[1].GetState())
	require.Equal(t, h.clock.Now().Add(secretOverlap).UnixMilli(), secrets[1].GetOverlapEndsAtUnixMs())

	h.clock.Advance(secretOverlap - time.Minute)
	status, _ := h.grant(id, first)
	require.Equal(t, http.StatusOK, status, "the replaced secret works during the overlap")
	status, _ = h.grant(id, second)
	require.Equal(t, http.StatusOK, status)

	h.clock.Advance(time.Minute)
	status, body := h.grant(id, first)
	require.Equal(t, http.StatusUnauthorized, status, "the overlap has ended")
	require.Equal(t, "invalid_client", body["error"])
	status, _ = h.grant(id, second)
	require.Equal(t, http.StatusOK, status)

	read, err := service.GetApplication(as("user", owner), &accountpublicv1.GetApplicationRequest{ApplicationId: id})
	require.NoError(t, err)
	require.Len(t, read.GetApplication().GetClientSecrets(), 1, "an ended overlap is no longer listed")
}

func TestRotatingDuringAnOverlapEndsTheEarlierSecret(t *testing.T) {
	h := newHarness(t)
	service := h.applicationService()
	owner := newPerson()
	registered, err := service.RegisterApplication(as("user", owner), &accountpublicv1.RegisterApplicationRequest{Name: "Support Desk"})
	require.NoError(t, err)
	id := registered.GetApplication().GetId()
	rotated, err := service.RotateClientSecret(as("user", owner), &accountpublicv1.RotateClientSecretRequest{ApplicationId: id})
	require.NoError(t, err)
	h.clock.Advance(time.Hour)

	again, err := service.RotateClientSecret(as("user", owner), &accountpublicv1.RotateClientSecretRequest{ApplicationId: id})
	require.NoError(t, err)
	require.Len(t, again.GetApplication().GetClientSecrets(), 2, "two secrets at most")

	status, _ := h.grant(id, registered.GetClientSecret())
	require.Equal(t, http.StatusUnauthorized, status)
	status, _ = h.grant(id, rotated.GetClientSecret())
	require.Equal(t, http.StatusOK, status)
	status, _ = h.grant(id, again.GetClientSecret())
	require.Equal(t, http.StatusOK, status)
}

func TestRevokedSecretIsRefusedAtOnce(t *testing.T) {
	h := newHarness(t)
	service := h.applicationService()
	owner := newPerson()
	registered, err := service.RegisterApplication(as("user", owner), &accountpublicv1.RegisterApplicationRequest{Name: "Support Desk"})
	require.NoError(t, err)
	id := registered.GetApplication().GetId()
	rotated, err := service.RotateClientSecret(as("user", owner), &accountpublicv1.RotateClientSecretRequest{ApplicationId: id})
	require.NoError(t, err)
	overlapping := rotated.GetApplication().GetClientSecrets()[1].GetId()

	revoked, err := service.RevokeClientSecret(as("user", owner), &accountpublicv1.RevokeClientSecretRequest{ApplicationId: id, ClientSecretId: overlapping})
	require.NoError(t, err)
	require.Len(t, revoked.GetApplication().GetClientSecrets(), 1)
	status, body := h.grant(id, registered.GetClientSecret())
	require.Equal(t, http.StatusUnauthorized, status)
	require.Equal(t, "invalid_client", body["error"])
	status, _ = h.grant(id, rotated.GetClientSecret())
	require.Equal(t, http.StatusOK, status)

	_, err = service.RevokeClientSecret(as("user", owner), &accountpublicv1.RevokeClientSecretRequest{ApplicationId: id, ClientSecretId: overlapping})
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err), "a revoked secret is not revoked again")
}

func TestOnlyTheOwnerRotatesOrRevokes(t *testing.T) {
	h := newHarness(t)
	service := h.applicationService()
	owner, other := newPerson(), newPerson()
	registered, err := service.RegisterApplication(as("user", owner), &accountpublicv1.RegisterApplicationRequest{Name: "Support Desk"})
	require.NoError(t, err)
	id := registered.GetApplication().GetId()
	secretID := registered.GetApplication().GetClientSecrets()[0].GetId()

	_, err = service.RotateClientSecret(as("user", other), &accountpublicv1.RotateClientSecretRequest{ApplicationId: id})
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	_, err = service.RevokeClientSecret(as("user", other), &accountpublicv1.RevokeClientSecretRequest{ApplicationId: id, ClientSecretId: secretID})
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	_, err = service.RotateClientSecret(as("application", id), &accountpublicv1.RotateClientSecretRequest{ApplicationId: id})
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err), "an Application never manages its own secrets")

	status, _ := h.grant(id, registered.GetClientSecret())
	require.Equal(t, http.StatusOK, status, "refused attempts changed nothing")
}
