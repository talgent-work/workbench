package query_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"

	"github.com/google/uuid"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/pkg/pluginconfig"
)

func TestSessionReadRequiresAuthorizationBeforeLoadingContext(t *testing.T) {
	denied := errors.New("access denied")
	handler := query.NewGetWorkHandler(nil, nil, authorizer{err: denied}, application.AssembleAgentWorkEntity, noPendingSessionInput)
	_, err := handler.Handle(context.Background(), query.GetWork{SubjectID: "other-user", WorkID: "private-session"})
	require.ErrorIs(t, err, denied)
}

func TestSessionReadReturnsIndependentCapabilitySnapshot(t *testing.T) {
	agent, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"owner"}, Purpose: "work", UsageScope: domain.UsageScope{Kind: "employee", Reference: "employee"}, Capabilities: domain.CapabilityConfiguration{RequiredPlugins: []string{"review"}}})
	require.NoError(t, err)
	session, err := domain.NewAgentWork(domain.WorkInput{Subject: domain.WorkSubject{Kind: "case", ID: "case"}, ProjectID: "project", WorkModel: domain.WorkModelSelection{ConfigurationID: "default"}, StartedBy: "owner", ID: uuid.Must(uuid.NewV7()).String(), RepresentedPerson: "user-home", WorkLayout: domain.DefaultWorkLayout(), AgentID: agent.ID, Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}, Plugins: []domain.SelectedPlugin{{Release: domain.PluginRelease{CatalogName: "talgent", Slug: "review", Source: "owned", Version: "1", RetrievalRef: "archive:review", NativeEntry: []byte(`{"name":"review","version":"1"}`), Parameters: pluginconfig.ParameterDeclarations{"mode": {Type: "string", Title: "Mode", Description: "Mode"}}}, Configuration: pluginconfig.Configuration{"mode": "strict"}}}}}, domain.StartAdmission{AdmittedAt: time.Now(), AgentID: agent.ID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", Kind: domain.ComputerCloud, DeskID: "desk-test"}, ModelConfigurationProvided: true})
	require.NoError(t, err)
	handler := query.NewGetWorkHandler(definitionRepository{agent}, repository{session}, authorizer{}, application.AssembleAgentWorkEntity, noPendingSessionInput)
	view, err := handler.Handle(context.Background(), query.GetWork{SubjectID: "owner", WorkID: session.ID})
	require.NoError(t, err)
	view.Capabilities.Plugins[0].Configuration["mode"] = "changed by caller"
	require.Equal(t, "strict", session.CapabilitySnapshot.Plugins[0].Configuration["mode"])
}

type authorizer struct{ err error }

func (a authorizer) AuthorizeWork(context.Context, string, string) error { return a.err }

type repository struct{ session *domain.AgentWork }

func (r repository) Get(context.Context, string) (*domain.AgentWork, error) { return r.session, nil }
func (repository) Save(context.Context, *domain.AgentWork) error            { return nil }
func (repository) FindBySubject(context.Context, string, domain.WorkSubject) (*domain.AgentWork, error) {
	return nil, nil
}

type definitionRepository struct{ agent *domain.AgentDefinition }

func (r definitionRepository) Get(context.Context, string) (*domain.AgentDefinition, error) {
	return r.agent, nil
}
func (definitionRepository) Save(context.Context, *domain.AgentDefinition) error { return nil }

func noPendingSessionInput(context.Context, string) (bool, error) {
	return false, nil
}
