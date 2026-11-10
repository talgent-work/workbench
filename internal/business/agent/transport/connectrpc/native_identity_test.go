package connectrpc_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	agentprivatev1 "github.com/talgent-work/contracts/gen/go/agent/private/v1"
	"github.com/talgent-work/contracts/gen/go/agent/private/v1/agentprivatev1connect"
	agentmodule "github.com/talgent-work/workbench/internal/business/agent"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// sandboxCredentials stands in for execution infrastructure: one credential per
// live sandbox of a Work, revoked when that sandbox is retired.
type sandboxCredentials struct {
	query.ExecutionQueryRepository
	live map[string]string
}

func (c *sandboxCredentials) VerifyWorkCredential(_ context.Context, workID, credential string) error {
	if issued, found := c.live[workID]; !found || issued != credential {
		return errors.New("credential rejected")
	}
	return nil
}

// The credential of an earlier process's sandbox is revoked
// with that sandbox; only the current sandbox's credential authenticates.
func TestWorkCredentialAuthenticatesOnlyTheCurrentLiveSandbox(t *testing.T) {
	ctx := context.Background()
	agent, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"owner"}, AgentName: "Fixture Agent", Purpose: "work", UsageScope: domain.UsageScope{Kind: "employee", Reference: "employee"}, Capabilities: domain.CapabilityConfiguration{}})
	require.NoError(t, err)
	session, err := domain.NewAgentWork(domain.WorkInput{Subject: domain.WorkSubject{Kind: "case", ID: "case"}, ProjectID: "project", WorkModel: domain.WorkModelSelection{ConfigurationID: "default"}, StartedBy: "owner", Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}}, ID: uuid.Must(uuid.NewV7()).String(), AgentID: agent.ID, RepresentedPerson: "authorized-home", WorkLayout: domain.DefaultWorkLayout()}, domain.StartAdmission{AdmittedAt: time.Now(), AgentID: agent.ID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", Kind: domain.ComputerCloud, DeskID: "desk-test"}, ModelConfigurationProvided: true})
	require.NoError(t, err)
	repo := &rpcSessions{items: map[string]*domain.AgentWork{}}
	require.NoError(t, repo.Save(ctx, session))
	credentials := &sandboxCredentials{live: map[string]string{session.ID: "first-sandbox-credential"}}
	app := &application.Application{Queries: application.Queries{VerifyWorkCredential: query.NewVerifyWorkCredentialHandler(&rpcAgents{items: map[string]*domain.AgentDefinition{agent.ID: agent}}, repo, credentials, application.AssembleWorkIdentity, query.NewAdmitAgentActionHandler(repo, awaitingResponse))}}
	mux := http.NewServeMux()
	agentmodule.RegisterRPCHandlers(mux, app, agentmodule.RPCConfig{ControlToken: "integration", FeedbackToken: "feedback"}, slog.Default(), nil)
	server := httptest.NewServer(mux)
	defer server.Close()
	client := agentprivatev1connect.NewAgentServiceClient(connect.NewClient(connecthttp.NewTransport(server.Client(), server.URL, connecthttp.WithReadMaxBytes(0))))
	first := &agentprivatev1.VerifyWorkCredentialRequest{WorkId: session.ID, Credential: "first-sandbox-credential"}
	_, err = client.VerifyWorkCredential(authenticated(ctx, "feedback"), first)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err), "a process credential does not authenticate an integration caller")
	verified, err := client.VerifyWorkCredential(authenticated(ctx, "integration"), first)
	require.NoError(t, err)
	require.Equal(t, "owner", verified.SubjectId, "a person's Agent acts for its Owner")
	require.Equal(t, session.ID, verified.WorkId)
	require.Equal(t, agent.ID, verified.AgentId)
	workspace := verified.GetWorkspace()
	require.Equal(t, "workspace", workspace.GetWorkspace().GetName(), "the Work's Session Workspace is its own Work Directory")
	require.Equal(t, "work", workspace.GetWorkspace().GetOwner().GetFacts()[0].GetCondition())
	require.Equal(t, session.ID, workspace.GetWorkspace().GetOwner().GetFacts()[0].GetValue())
	mounted := map[string]string{}
	for _, mount := range workspace.GetMounts() {
		mounted[mount.GetPath()] = mount.GetDirectory().GetOwner().GetFacts()[0].GetValue() + "/" + mount.GetDirectory().GetName()
	}
	require.Equal(t, map[string]string{"inputs": "case:case/inputs", "knowledge": "project/knowledge"}, mounted, "the Directories mounted inside it follow its Subject and Project")
	require.Equal(t, "project", verified.ProjectId)
	require.Equal(t, "case", verified.GetSubject().GetKind())
	require.Equal(t, "case", verified.GetSubject().GetId())
	require.Equal(t, "work", verified.Purpose)
	_, err = client.VerifyWorkCredential(authenticated(ctx, "integration"), &agentprivatev1.VerifyWorkCredentialRequest{WorkId: session.ID, Credential: first.Credential, RequireActing: true})
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err), "a Work not yet Working does not act")
	for _, invalid := range []*agentprivatev1.VerifyWorkCredentialRequest{
		{WorkId: session.ID, Credential: "forged"},
		{WorkId: "other-work", Credential: first.Credential},
	} {
		_, err = client.VerifyWorkCredential(authenticated(ctx, "integration"), invalid)
		require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	}
	// The earlier sandbox is retired and a resumed process runs in a new one.
	credentials.live[session.ID] = "second-sandbox-credential"
	_, err = client.VerifyWorkCredential(authenticated(ctx, "integration"), first)
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err), "an earlier sandbox's credential grants nothing")
	_, err = client.VerifyWorkCredential(authenticated(ctx, "integration"), &agentprivatev1.VerifyWorkCredentialRequest{WorkId: session.ID, Credential: "second-sandbox-credential"})
	require.NoError(t, err)
}

func TestInvalidWorkCredentialDoesNotReadWorkData(t *testing.T) {
	handler := query.NewVerifyWorkCredentialHandler(nil, nil, &sandboxCredentials{}, application.AssembleWorkIdentity, nil)
	_, err := handler.Handle(context.Background(), query.VerifyWorkCredential{WorkID: "private-work", Credential: "forged"})
	require.Error(t, err)
}

// awaitingResponse reports every Work as awaiting its person's response; it
// is read only for a Working Work.
func awaitingResponse(context.Context, string) (bool, error) { return true, nil }

// An action is admitted while the Work acts: Working, its person's required
// response resolved. A reply resolving within moments lets it through; one
// that stays unresolved is refused.
func TestWorkCredentialForAnActionRequiresTheWorkToAct(t *testing.T) {
	ctx := context.Background()
	agent, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"owner"}, AgentName: "Fixture Agent", Purpose: "work", UsageScope: domain.UsageScope{Kind: "employee", Reference: "employee"}, Capabilities: domain.CapabilityConfiguration{}})
	require.NoError(t, err)
	session, err := domain.NewAgentWork(domain.WorkInput{WorkModel: domain.WorkModelSelection{ConfigurationID: "default"}, StartedBy: "owner", Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}}, ID: uuid.Must(uuid.NewV7()).String(), AgentID: agent.ID, RepresentedPerson: "home", WorkLayout: domain.DefaultWorkLayout()}, domain.StartAdmission{AdmittedAt: time.Now(), AgentID: agent.ID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", Kind: domain.ComputerCloud, DeskID: "desk-test"}, ModelConfigurationProvided: true})
	require.NoError(t, err)
	session.State = domain.WorkWorking
	repo := &rpcSessions{items: map[string]*domain.AgentWork{}}
	require.NoError(t, repo.Save(ctx, session))
	verify := func(awaiting func(context.Context, string) (bool, error)) error {
		handler := query.NewVerifyWorkCredentialHandler(&rpcAgents{items: map[string]*domain.AgentDefinition{agent.ID: agent}}, repo, &sandboxCredentials{live: map[string]string{session.ID: "credential"}}, application.AssembleWorkIdentity, query.NewAdmitAgentActionHandler(repo, awaiting))
		_, err := handler.Handle(ctx, query.VerifyWorkCredential{WorkID: session.ID, Credential: "credential", RequireActing: true})
		return err
	}
	require.NoError(t, verify(func(context.Context, string) (bool, error) { return false, nil }))
	reads := 0
	require.NoError(t, verify(func(context.Context, string) (bool, error) { reads++; return reads < 3, nil }), "a reply resolving within moments")
	require.ErrorIs(t, verify(awaitingResponse), domain.ErrWorkNotActing, "a Work awaiting its person does not act")
}
