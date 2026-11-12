package application_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"
)

func TestServiceAccessResultsPreserveOwnerAndReturnIndependentObservations(t *testing.T) {
	f := newServiceAccessFixture(t)
	authorization := &thinkingAccess{}
	reader := &serviceObservationReader{results: []query.ServiceAccessObservation{{Service: "github", Target: "github-api", Status: "unavailable", Reason: "revoked"}}}
	app := &application.Application{Works: serviceAccessWorks{work: f.work}, WorkPrincipals: authorization, ExecutionQueries: reader}
	result, err := app.GetWorkServiceAccess(t.Context(), application.ControlWork{ActorID: "viewer", WorkID: f.work.ID})
	require.NoError(t, err)
	require.Equal(t, "owner", result.OwnerID)
	require.Equal(t, []string{"github"}, result.SelectedServices)
	require.Equal(t, reader.results, result.Observations)
	require.Equal(t, "viewer", authorization.subject, "the requester itself must be the owner")
	result.Observations[0].Reason = "changed"
	result.SelectedServices[0] = "changed"
	require.Equal(t, "revoked", reader.results[0].Reason)
	require.Equal(t, "github", f.work.AuthorizationSelection.Authorizations[0].Service)
}

func TestServiceAccessResultsDoNotHideAccessOrStorageFailures(t *testing.T) {
	f := newServiceAccessFixture(t)
	authorization := &thinkingAccess{err: access.ErrAccessDenied}
	reader := &serviceObservationReader{err: errors.New("storage unavailable")}
	app := &application.Application{Works: serviceAccessWorks{work: f.work}, WorkPrincipals: authorization, ExecutionQueries: reader}
	_, err := app.GetWorkServiceAccess(t.Context(), application.ControlWork{WorkID: " "})
	require.ErrorIs(t, err, domain.ErrInvalidWork)
	_, err = app.GetWorkServiceAccess(t.Context(), application.ControlWork{ActorID: "outsider", WorkID: f.work.ID})
	require.ErrorIs(t, err, access.ErrAccessDenied)
	require.Zero(t, reader.calls)
	authorization.err = nil
	_, err = app.GetWorkServiceAccess(t.Context(), application.ControlWork{ActorID: "reader", WorkID: f.work.ID})
	require.ErrorIs(t, err, reader.err)
}

type serviceObservationReader struct {
	query.ExecutionQueryRepository
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

// serviceAccessFixture is a Work whose owner selected a GitHub authorization.
type serviceAccessFixture struct{ work *domain.AgentWork }

func newServiceAccessFixture(t *testing.T) *serviceAccessFixture {
	t.Helper()
	agentID := uuid.NewString()
	work, err := domain.NewAgentWork(domain.WorkInput{Subject: domain.WorkSubject{Kind: "case", ID: "case"}, ProjectID: "project", StartedBy: "owner", Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}}, ID: uuid.NewString(), AgentID: agentID,
		WorkModel:         domain.WorkModelSelection{ConfigurationID: "default"},
		RepresentedPerson: "owner", WorkLayout: domain.DefaultWorkLayout(),
		AuthorizationInput: domain.WorkAuthorizationInput{OwnerID: "owner", Available: []domain.PersonalAuthorization{{OwnerID: "owner", AuthorizationID: "b5f00000-0000-4000-8000-000000000382", Provider: "github", Service: "github"}}},
	}, domain.StartAdmission{AdmittedAt: time.Now(), AgentID: agentID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", Kind: domain.ComputerCloud, DeskID: "desk-test"}, ModelConfigurationProvided: true})
	require.NoError(t, err)
	return &serviceAccessFixture{work: work}
}

// serviceAccessWorks holds the one Work.
type serviceAccessWorks struct {
	domain.AgentWorkRepository
	work *domain.AgentWork
}

func (s serviceAccessWorks) Get(_ context.Context, id string) (*domain.AgentWork, error) {
	if id != s.work.ID {
		return nil, domain.ErrWorkNotFound
	}
	return s.work, nil
}
