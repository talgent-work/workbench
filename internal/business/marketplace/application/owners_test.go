package application_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

type ownership map[string]application.OwnedApplication

func (o ownership) GetApplication(_ context.Context, id string) (application.OwnedApplication, error) {
	if id == "broken" {
		return application.OwnedApplication{}, errors.New("account unavailable")
	}
	found, ok := o[id]
	if !ok {
		return application.OwnedApplication{}, application.ErrApplicationUnknown
	}
	return found, nil
}

func TestOwnersResolveWhoseAuthorizationsARequestReaches(t *testing.T) {
	owners := application.NewOwners(ownership{
		"app-1":   {ID: "app-1", OwnerUserID: "alice"},
		"removed": {ID: "removed", OwnerUserID: "alice", Removed: true},
	})
	ctx := context.Background()
	cases := []struct {
		name        string
		caller      application.Caller
		application string
		owner       string
		err         error
	}{
		{name: "a person reaches their own", caller: application.Caller{UserID: "alice"}, owner: "alice"},
		{name: "an Application Owner reaches the Application", caller: application.Caller{UserID: "alice"}, application: "app-1", owner: "app-1"},
		{name: "another person is refused", caller: application.Caller{UserID: "bob"}, application: "app-1", err: domain.ErrPermissionDenied},
		{name: "a removed Application is refused", caller: application.Caller{UserID: "alice"}, application: "removed", err: domain.ErrPermissionDenied},
		{name: "an unknown Application is refused without revealing it", caller: application.Caller{UserID: "alice"}, application: "nobody", err: domain.ErrPermissionDenied},
		{name: "an Application reaches itself", caller: application.Caller{ApplicationID: "app-1"}, owner: "app-1"},
		{name: "an Application may name itself", caller: application.Caller{ApplicationID: "app-1"}, application: "app-1", owner: "app-1"},
		{name: "an Application never reaches another", caller: application.Caller{ApplicationID: "app-1"}, application: "app-2", err: domain.ErrPermissionDenied},
		{name: "no identity is unauthenticated", caller: application.Caller{}, err: application.ErrUnauthenticated},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			owner, err := owners.Resolve(ctx, tc.caller, tc.application)
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.owner, owner)
		})
	}
}

func TestOwnersResolveReportsAnUnavailableAccount(t *testing.T) {
	_, err := application.NewOwners(ownership{}).Resolve(context.Background(), application.Caller{UserID: "alice"}, "broken")
	require.Error(t, err)
	require.NotErrorIs(t, err, domain.ErrPermissionDenied)
}
