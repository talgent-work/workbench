package command_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

func TestRepositoryAccessUnavailableCredentialsDoNotFailWork(t *testing.T) {
	for _, scenario := range []string{"empty token", "empty username", "invalid username", "expired", "revoked", "reconnect_required", "provider failure"} {
		t.Run(scenario, func(t *testing.T) {
			f := newRepositoryAccessFixture(t)
			reason := "unavailable"
			switch scenario {
			case "empty token":
				f.token = ""
			case "empty username":
				f.username = ""
			case "invalid username":
				f.username = "user:password"
			case "expired":
				f.expiresAt = time.Now().Add(-time.Minute)
			case "revoked", "reconnect_required":
				reason = scenario
				f.supplyError = &command.ServiceCredentialUnavailable{Reason: reason}
			case "provider failure":
				f.supplyError = errors.New("provider error with sensitive details")
			}
			selection := f.work.AuthorizationSelection.Clone()
			result, err := f.app.Resolve(t.Context(), "owner", f.work.ID)
			require.NoError(t, err)
			require.Empty(t, result.Credentials)
			require.Equal(t, []command.RepositoryAccessFailure{{RepositoryID: "123", Reason: reason, Temporary: reason == "unavailable"}}, result.Unavailable, "only a refusal Marketplace confirmed is not temporary")
			require.Equal(t, domain.WorkStarting, f.work.State)
			require.Equal(t, selection, f.work.AuthorizationSelection)

			f.token, f.username, f.expiresAt, f.supplyError = "recovered-token", "x-access-token", time.Now().Add(time.Hour), nil
			result, err = f.app.Resolve(t.Context(), "owner", f.work.ID)
			require.NoError(t, err)
			require.Empty(t, result.Unavailable)
			require.Len(t, result.Credentials, 1)
			require.Equal(t, "recovered-token", result.Credentials[0].Password)
			require.Equal(t, selection.Authorizations[0].AuthorizationID, result.Credentials[0].AuthorizationID)
		})
	}
}

func TestRepositoryAccessWithoutAuthorizationRetainsPlanWithoutSupply(t *testing.T) {
	f := newRepositoryAccessFixture(t, true)
	refs, err := f.app.Plan(t.Context(), "owner", f.work.ID)
	require.NoError(t, err)
	require.Len(t, refs, 1)
	require.Empty(t, refs[0].AuthorizationID)
	for range 2 {
		result, err := f.app.Resolve(t.Context(), "owner", f.work.ID)
		require.NoError(t, err)
		require.Empty(t, result.Credentials)
		require.Equal(t, []command.RepositoryAccessFailure{{RepositoryID: "123", Reason: "not_selected"}}, result.Unavailable)
	}
	require.Empty(t, f.supplied, "an unselected authorization is never supplied")
	require.Equal(t, domain.WorkStarting, f.work.State)
}

func TestRepositoryAccessCancellationIsNotCapabilityDegradation(t *testing.T) {
	f := newRepositoryAccessFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	f.duringSupply = cancel
	f.supplyError = context.Canceled
	result, err := f.app.Resolve(ctx, "owner", f.work.ID)
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, result)
}
