//go:build integration

package storage

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	storageprivatev1 "github.com/talgent-work/contracts/gen/go/storage/private/v1"
	"github.com/talgent-work/contracts/gen/go/storage/private/v1/storageprivatev1connect"
	agentmodule "github.com/talgent-work/workbench/internal/business/agent"
	agentapplication "github.com/talgent-work/workbench/internal/business/agent/application"
	agentquery "github.com/talgent-work/workbench/internal/business/agent/application/query"
	agentdomain "github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/pkg/authn"
	"github.com/talgent-work/workbench/internal/pkg/nativeauth"
	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"
)

// A Work's identity reaches Storage as Agent assembles it: a Work started on
// a Managed Computer carries no Session Workspace, though its creating
// caller named one, so Storage takes the content it sends; a Cloud Work
// carries its workspace and publishes only from it.
func TestAManagedWorkIdentityCarriesNoWorkspaceSoItsSentContentIsTaken(t *testing.T) {
	f := newFixture(t)
	space := f.open("case-1")
	agent, err := agentdomain.NewAgentDefinition(agentdomain.AgentDefinitionInput{Owners: []string{"owner"}, AgentName: "Fixture Agent", Purpose: "work", UsageScope: agentdomain.UsageScope{Kind: "employee", Reference: "owner"}, HomeBinding: agentdomain.HomeBinding{OwnerID: "owner-home", Namespace: "project"}})
	require.NoError(t, err)
	works := &identityWorks{items: map[string]*agentdomain.AgentWork{}}
	start := func(managed bool) string {
		work, err := agentdomain.NewAgentWork(agentdomain.WorkInput{
			StartedBy: "owner", ID: uuid.Must(uuid.NewV7()).String(), AgentID: agent.ID, ComputerManaged: managed,
			Subject: agentdomain.WorkSubject{Kind: "case", ID: "case-1"}, ProjectID: "project-1", WorkModel: agentdomain.WorkModelSelection{ConfigurationID: "default"},
			Capabilities:       agentdomain.CapabilitySnapshot{Connectors: []agentdomain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}},
			WorkspaceReference: "named-workspace", HomeBinding: agent.HomeBinding,
		}, agentdomain.StartAdmission{AdmittedAt: time.Now(), AgentID: agent.ID, DefinitionAdmitted: true, ModelConfigurationProvided: true, Seat: agentdomain.ComputerSeat{ComputerID: uuid.NewString(), Kind: map[bool]agentdomain.ComputerKind{false: agentdomain.ComputerCloud, true: agentdomain.ComputerManaged}[managed], DeskID: "desk"}})
		require.NoError(t, err)
		works.items[work.ID] = work
		return work.ID
	}
	managed, cloud := start(true), start(false)

	app := &agentapplication.Application{Queries: agentapplication.Queries{VerifyWorkCredential: agentquery.NewVerifyWorkCredentialHandler(identityDefinitions{agent}, works, identityCredentials{}, agentapplication.AssembleWorkIdentity, func(context.Context, string) (bool, error) { return false, nil })}}
	agentMux := http.NewServeMux()
	agentmodule.RegisterRPCHandlers(agentMux, app, agentmodule.RPCConfig{ControlToken: "control", FeedbackToken: "feedback"}, slog.Default(), nil)
	agentServer := httptest.NewServer(agentMux)
	t.Cleanup(agentServer.Close)
	live, err := nativeauth.NewLiveWorkClient(agentServer.Client(), agentServer.URL, "control")
	require.NoError(t, err)

	storageMux := http.NewServeMux()
	mountArtifactService(storageMux, f.app, slog.Default())
	storageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The native API resolves the Work's credential through Agent.
		process, err := live.VerifyNativeProcess(r.Context(), r.Header.Get("X-Work"), "credential")
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		storageMux.ServeHTTP(w, r.WithContext(authn.WithNativeProcess(r.Context(), process)))
	}))
	t.Cleanup(storageServer.Close)
	client := storageprivatev1connect.NewArtifactServiceClient(connect.NewClient(connecthttp.NewTransport(storageServer.Client(), storageServer.URL)))
	publish := func(workID, name string) error {
		ctx, info := connect.NewClientContext(t.Context())
		info.RequestHeader().Set(authn.HeaderSpaceToken, space.SpaceToken)
		info.RequestHeader().Set("X-Work", workID)
		_, err := client.CreateArtifact(ctx, &storageprivatev1.CreateArtifactRequest{Name: name, Content: []byte("sent")})
		return err
	}

	process, err := live.VerifyNativeProcess(t.Context(), managed, "credential")
	require.NoError(t, err)
	require.Empty(t, process.WorkspaceReference, "a Managed Work has no Session Workspace")
	require.NoError(t, publish(managed, "managed.md"))
	require.Contains(t, f.files("case-1"), "artifact/managed.md")

	process, err = live.VerifyNativeProcess(t.Context(), cloud, "credential")
	require.NoError(t, err)
	require.Equal(t, "named-workspace", process.WorkspaceReference)
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(publish(cloud, "cloud.md")), "a Cloud Work publishes from its workspace")
	require.NotContains(t, f.files("case-1"), "artifact/cloud.md")
}

type identityDefinitions struct{ agent *agentdomain.AgentDefinition }

func (d identityDefinitions) Get(_ context.Context, id string) (*agentdomain.AgentDefinition, error) {
	if id != d.agent.ID {
		return nil, agentdomain.ErrAgentDefinitionNotFound
	}
	return d.agent, nil
}

func (identityDefinitions) Save(context.Context, *agentdomain.AgentDefinition) error {
	return errors.New("not saved")
}

type identityWorks struct {
	items map[string]*agentdomain.AgentWork
}

func (w *identityWorks) Get(_ context.Context, id string) (*agentdomain.AgentWork, error) {
	work, ok := w.items[id]
	if !ok {
		return nil, agentdomain.ErrWorkNotFound
	}
	return work, nil
}

func (w *identityWorks) FindBySubject(_ context.Context, agentID string, subject agentdomain.WorkSubject) (*agentdomain.AgentWork, error) {
	for _, work := range w.items {
		if work.AgentID == agentID && work.Subject == subject {
			return work, nil
		}
	}
	return nil, nil
}

func (w *identityWorks) Save(context.Context, *agentdomain.AgentWork) error {
	return errors.New("not saved")
}

// identityCredentials admits each Work's one live credential.
type identityCredentials struct{}

func (identityCredentials) VerifyWorkCredential(_ context.Context, _, credential string) error {
	if credential != "credential" {
		return errors.New("credential rejected")
	}
	return nil
}
