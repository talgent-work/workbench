package application_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

func TestServiceAccessResultsPreserveOwnerAndReturnIndependentObservations(t *testing.T) {
	f := newRepositoryAccessFixture(t)
	authorization := &thinkingAccess{}
	reader := &serviceObservationReader{results: []query.ServiceAccessObservation{{Service: "github", Target: "github-api", Status: "unavailable", Reason: "revoked"}}}
	app := &application.Application{Works: repositoryWorkStore{f: f}, WorkPrincipals: authorization, ServiceAccessResults: reader}
	result, err := app.GetWorkServiceAccess(t.Context(), application.ControlWork{ActorUserID: "viewer", WorkID: f.work.ID})
	require.NoError(t, err)
	require.Equal(t, "owner", result.OwnerUserID)
	require.Equal(t, []string{"github"}, result.SelectedServices)
	require.Equal(t, reader.results, result.Observations)
	require.Equal(t, "viewer", authorization.subject, "the requester itself must be the owner")
	result.Observations[0].Reason = "changed"
	result.SelectedServices[0] = "changed"
	require.Equal(t, "revoked", reader.results[0].Reason)
	require.Equal(t, "github", f.work.AuthorizationSelection.Authorizations[0].Service)
}

func TestServiceAccessResultsDoNotHideAccessOrStorageFailures(t *testing.T) {
	f := newRepositoryAccessFixture(t)
	authorization := &thinkingAccess{err: access.ErrAccessDenied}
	reader := &serviceObservationReader{err: errors.New("storage unavailable")}
	app := &application.Application{Works: repositoryWorkStore{f: f}, WorkPrincipals: authorization, ServiceAccessResults: reader}
	_, err := app.GetWorkServiceAccess(t.Context(), application.ControlWork{WorkID: " "})
	require.ErrorIs(t, err, domain.ErrInvalidWork)
	_, err = app.GetWorkServiceAccess(t.Context(), application.ControlWork{ActorUserID: "outsider", WorkID: f.work.ID})
	require.ErrorIs(t, err, access.ErrAccessDenied)
	require.Zero(t, reader.calls)
	authorization.err = nil
	_, err = app.GetWorkServiceAccess(t.Context(), application.ControlWork{ActorUserID: "reader", WorkID: f.work.ID})
	require.ErrorIs(t, err, reader.err)
}

type serviceObservationReader struct {
	results []query.ServiceAccessObservation
	err     error
	calls   int
}

func (r *serviceObservationReader) ReadServiceAccess(context.Context, string) ([]query.ServiceAccessObservation, error) {
	r.calls++
	return r.results, r.err
}

func (r *serviceObservationReader) ReadRepositoryPreparation(context.Context, string) ([]query.RepositoryPreparation, error) {
	return nil, r.err
}
