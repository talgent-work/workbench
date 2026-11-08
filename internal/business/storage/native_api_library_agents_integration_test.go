//go:build integration

package storage

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"xorm.io/xorm"

	"github.com/talgent-work/contracts/gen/go/storage/private/v1/storageprivatev1connect"
	agentapplication "github.com/talgent-work/workbench/internal/business/agent/application"
	agentcommand "github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/personalwork"
	agentquery "github.com/talgent-work/workbench/internal/business/agent/application/query"
	agentdomain "github.com/talgent-work/workbench/internal/business/agent/domain"
	agentinfrastructure "github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	agentstorage "github.com/talgent-work/workbench/internal/business/agent/infrastructure/storage"
	"github.com/talgent-work/workbench/internal/business/storage/application"
	storageconnect "github.com/talgent-work/workbench/internal/business/storage/transport/connectrpc"
	"github.com/talgent-work/workbench/internal/testsupport/connecttest"
	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"
)

// libraryAgents is Agent over its own MySQL schema: people's employee
// definitions and their Works, and the Personal Library read through
// Storage's File Space Contract served by the fixture.
type libraryAgents struct {
	t           *testing.T
	f           *fixture
	db          *xorm.Engine
	app         *agentapplication.Application
	definitions *agentinfrastructure.AgentDefinitionRepository
	works       *agentinfrastructure.AgentWorkRepository
}

// libraryWork is one started Work, its File Space and its workspace.
type libraryWork struct {
	id, workspace string
	space         application.OpenedSpace
}

const libraryAgentToken = "agent-file-spaces"

func newLibraryAgents(t *testing.T, f *fixture) *libraryAgents {
	t.Helper()
	db, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	definitions := agentinfrastructure.NewAgentDefinitionRepository(db, time.Now)
	lookup := agentinfrastructure.NewAgentDefinitionLookup(db)
	works := agentinfrastructure.NewAgentWorkRepository(db, time.Now)
	app := &agentapplication.Application{
		Definitions: definitions, DefinitionLookup: lookup, Works: works, WorkViews: agentinfrastructure.NewWorkQueryRepository(db),
		Commands: agentapplication.Commands{EnsureAgentDefinition: agentcommand.NewEnsureAgentDefinitionHandler(definitions, lookup)},
		Queries: agentapplication.Queries{VerifyWorkCredential: agentquery.NewVerifyWorkCredentialHandler(definitions, works, identityCredentials{},
			agentapplication.AssembleWorkIdentity, func(context.Context, string) (bool, error) { return false, nil })},
	}
	return &libraryAgents{t: t, f: f, db: db, app: app, definitions: definitions, works: works}
}

// start registers a Work of the person's employee definition, with its own
// File Space, under projectID when one is given.
func (a *libraryAgents) start(person, workspace, projectID string) libraryWork {
	a.t.Helper()
	ctx := a.t.Context()
	initialized, err := a.app.EnsureEmployeeAgent(ctx, person, person)
	require.NoError(a.t, err)
	definition, err := a.definitions.Get(ctx, initialized.AgentID)
	require.NoError(a.t, err)
	work := libraryWork{id: uuid.Must(uuid.NewV7()).String(), workspace: workspace}
	work.space = a.f.open(work.id)
	started, err := agentdomain.NewAgentWork(agentdomain.WorkInput{
		StartedBy: person, ID: work.id, AgentID: definition.ID, ProjectID: projectID, WorkModel: agentdomain.WorkModelSelection{ConfigurationID: "default"},
		WorkspaceReference: workspace, HomeBinding: definition.HomeBinding,
		InputSource: agentdomain.WorkInputSource{Reference: "file-space/" + work.id, SpaceToken: work.space.SpaceToken},
	}, agentdomain.StartAdmission{AdmittedAt: time.Now(), AgentID: definition.ID, DefinitionAdmitted: true, ModelConfigurationProvided: true, Seat: agentdomain.ComputerSeat{ComputerID: uuid.NewString(), Kind: agentdomain.ComputerCloud, DeskID: "desk"}})
	require.NoError(a.t, err)
	require.Empty(a.t, started.CapabilitySnapshot.Connectors, "the Work starts without a platform Connector")
	require.NoError(a.t, a.works.RegisterWork(ctx, started, agentcommand.WorkRegistration{
		SubjectID: person, Request: agentquery.StartRequest{ID: uuid.NewString(), Kind: agentquery.StartKindStart, WorkID: started.ID}, WorkspaceReference: workspace, DefinitionVersion: definition.Version,
	}))
	return work
}

// personalWorks is Agent's Personal Library over Storage's File Space
// Contract, reached over RPC as in production.
func (a *libraryAgents) personalWorks() *personalwork.PersonalWorks {
	a.t.Helper()
	mux := http.NewServeMux()
	mux.Handle("/"+storageprivatev1connect.FileSpaceServiceName+"/", connecttest.Handler(func(s *connect.Server) {
		storageprivatev1connect.RegisterFileSpaceServiceHandler(s, storageconnect.NewFileSpaceHandler(a.f.app, []string{libraryAgentToken}, nil))
	}))
	server := httptest.NewServer(mux)
	a.t.Cleanup(server.Close)
	spaces, err := agentstorage.NewClient(server.Client(), agentstorage.Config{Address: server.URL, Token: libraryAgentToken})
	require.NoError(a.t, err)
	return personalwork.New(a.app, agentinfrastructure.NewWorkQueryRepository(a.db), spaces, nil)
}
