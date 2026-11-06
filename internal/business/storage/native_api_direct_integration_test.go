//go:build integration

package storage

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	agentpublicv1 "github.com/talgent-work/workbench/gen/go/talgent/agent/public/v1"
	"github.com/talgent-work/workbench/gen/go/talgent/agent/public/v1/agentpublicv1connect"
	agentmodule "github.com/talgent-work/workbench/internal/business/agent"
	agentapplication "github.com/talgent-work/workbench/internal/business/agent/application"
	agentquery "github.com/talgent-work/workbench/internal/business/agent/application/query"
	agentdomain "github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/storage/application"
	"github.com/talgent-work/workbench/internal/business/storage/domain"
	storagehttp "github.com/talgent-work/workbench/internal/business/storage/transport/http"
	"github.com/talgent-work/workbench/pkg/authn"
	"github.com/talgent-work/workbench/pkg/nativeapi"
	"github.com/talgent-work/workbench/pkg/nativeauth"
)

// Without a platform Connector, a Work's Agent calls the controlplane's
// native API itself, as the core runtime plugin's using-platform-api skill
// teaches: plain Connect JSON over HTTP, with the headers its sandbox's
// Credential Proxy adds. It publishes an Artifact from its workspace, finds
// it in the Personal Library and downloads it through the Access Link it is
// issued. Agent verifies the Work credential and Storage keeps the files;
// the Library's procedures stand in for Agent's, issuing Storage's links.
func TestAWorkPublishesAndDownloadsThroughTheNativeAPIWithoutAGateway(t *testing.T) {
	f := newFixture(t)
	agent, err := agentdomain.NewAgentDefinition(agentdomain.AgentDefinitionInput{Owners: []string{"owner"}, AgentName: "Fixture Agent", Purpose: agentdomain.EmployeeWorkPurpose, UsageScope: agentdomain.UsageScope{Kind: "employee", Reference: "owner"}, HomeBinding: agentdomain.HomeBinding{OwnerID: "owner-home", Namespace: "personal"}})
	require.NoError(t, err)
	workID := uuid.Must(uuid.NewV7()).String()
	space := f.open(workID)
	work, err := agentdomain.NewAgentWork(t.Context(), agentdomain.WorkInput{
		StartedBy: "owner", ID: workID, AgentID: agent.ID, WorkModel: agentdomain.WorkModelSelection{ConfigurationID: "default"},
		WorkspaceReference: "personal-workspace", HomeBinding: agent.HomeBinding,
		InputSource: agentdomain.WorkInputSource{Reference: "file-space/" + workID, SpaceToken: space.SpaceToken},
	}, agentdomain.StartAdmission{AdmittedAt: time.Now(), AgentID: agent.ID, DefinitionAdmitted: true, ModelConfigurationProvided: true, Seat: agentdomain.ComputerSeat{ComputerID: uuid.NewString(), DeskID: "desk"}}, identityExecution{})
	require.NoError(t, err)
	require.Empty(t, work.CapabilitySnapshot.Connectors, "the Work starts without a platform Connector")
	works := &identityWorks{items: map[string]*agentdomain.AgentWork{workID: work}}

	app := &agentapplication.Application{Queries: agentapplication.Queries{VerifyWorkCredential: agentquery.NewVerifyWorkCredentialHandler(identityDefinitions{agent}, works, identityCredentials{}, agentapplication.AssembleWorkIdentity, func(context.Context, string) (bool, error) { return false, nil })}}
	agentMux := http.NewServeMux()
	agentmodule.RegisterRPCHandlers(agentMux, app, agentmodule.RPCConfig{ControlToken: "control", FeedbackToken: "feedback"}, slog.Default(), nil)
	agentServer := httptest.NewServer(agentMux)
	t.Cleanup(agentServer.Close)
	live, err := nativeauth.NewLiveWorkClient(agentServer.Client(), agentServer.URL, "control")
	require.NoError(t, err)

	business := http.NewServeMux()
	business.Handle(artifactServiceHandler(f.app))
	storagehttp.Register(business, f.app, fixtureMaxUploadBytes)
	library := libraryStandIn{app: f.app, workID: workID}
	business.Handle(agentpublicv1connect.NewPersonalWorkServiceHandler(library))
	business.Handle(agentpublicv1connect.NewWorkFileServiceHandler(library))
	native, err := nativeapi.NewHandler(business, "", live, nativeapi.Calls{SpaceTokenProcedures: AgentArtifactProcedures},
		nativeapi.ReadProcedures{Procedures: agentmodule.AgentLibraryProcedures, Verifier: live})
	require.NoError(t, err)
	api := httptest.NewServer(native)
	t.Cleanup(api.Close)

	// What the Credential Proxy adds to every request to the native API.
	injected := map[string]string{authn.HeaderNativeWorkID: workID, authn.HeaderNativeCredential: "credential", authn.HeaderSpaceToken: space.SpaceToken}
	call := func(procedure, body string, headers map[string]string) (int, map[string]any) {
		request, err := http.NewRequest(http.MethodPost, api.URL+procedure, strings.NewReader(body))
		require.NoError(t, err)
		request.Header.Set("Content-Type", "application/json")
		for key, value := range headers {
			request.Header.Set(key, value)
		}
		response, err := api.Client().Do(request)
		require.NoError(t, err)
		defer response.Body.Close()
		var decoded map[string]any
		require.NoError(t, json.NewDecoder(response.Body).Decode(&decoded))
		return response.StatusCode, decoded
	}

	f.workspaceFile("personal-workspace", "dist/report.md", "# Report")
	status, published := call("/talgent.storage.private.v1.ArtifactService/CreateArtifact", `{"name":"report.md","path":"dist/report.md"}`, injected)
	require.Equal(t, http.StatusOK, status, published)
	require.Equal(t, "report.md", published["outcome"].(map[string]any)["artifact"].(map[string]any)["name"])
	require.Contains(t, f.files(workID), "artifact/report.md")
	status, refused := call("/talgent.storage.private.v1.ArtifactService/CreateArtifact", `{"name":"report.md","path":"dist/report.md"}`, injected)
	require.Equal(t, http.StatusConflict, status, "an existing name is refused: replace it on purpose")
	require.Equal(t, "already_exists", refused["code"])
	status, _ = call("/talgent.storage.private.v1.ArtifactService/CreateArtifact", `{"name":"other.md","path":"dist/report.md"}`, map[string]string{authn.HeaderNativeWorkID: workID, authn.HeaderNativeCredential: "guess"})
	require.Equal(t, http.StatusUnauthorized, status, "only the Work's current credential is admitted")

	status, found := call("/talgent.agent.public.v1.PersonalWorkService/ListLibraryFiles", `{"nameQuery":"report"}`, injected)
	require.Equal(t, http.StatusOK, status, found)
	file := found["files"].([]any)[0].(map[string]any)
	require.Equal(t, map[string]any{"workId": workID, "name": "report.md"}, map[string]any{"workId": file["workId"], "name": file["name"]})
	status, issued := call("/talgent.agent.public.v1.WorkFileService/CreateWorkFileAccessLink",
		`{"workId":"`+workID+`","kind":"WORK_FILE_KIND_ARTIFACT","name":"report.md","disposition":"WORK_FILE_ACCESS_DISPOSITION_DOWNLOAD"}`, injected)
	require.Equal(t, http.StatusOK, status, issued)
	accessPath := issued["accessPath"].(string)
	require.True(t, strings.HasPrefix(accessPath, nativeapi.AccessLinkPath+"?"), accessPath)

	download := func(headers map[string]string) (int, string) {
		request, err := http.NewRequest(http.MethodGet, api.URL+accessPath, nil)
		require.NoError(t, err)
		for key, value := range headers {
			request.Header.Set(key, value)
		}
		response, err := api.Client().Do(request)
		require.NoError(t, err)
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		require.NoError(t, err)
		return response.StatusCode, string(body)
	}
	status, content := download(map[string]string{authn.HeaderNativeWorkID: workID, authn.HeaderNativeCredential: "credential"})
	require.Equal(t, http.StatusOK, status, content)
	require.Equal(t, "# Report", content)
	status, _ = download(nil)
	require.Equal(t, http.StatusUnauthorized, status, "a link is read only with the Work's credential")
}

// libraryStandIn serves the Library's procedures for one Work's owner over
// Storage: its Artifacts, and Storage's Access Links to them.
type libraryStandIn struct {
	agentpublicv1connect.UnimplementedPersonalWorkServiceHandler
	agentpublicv1connect.UnimplementedWorkFileServiceHandler
	app    *application.Application
	workID string
}

func (l libraryStandIn) ListLibraryFiles(ctx context.Context, request *connect.Request[agentpublicv1.ListLibraryFilesRequest]) (*connect.Response[agentpublicv1.ListLibraryFilesResponse], error) {
	files, err := l.app.ListFiles(ctx, l.workID)
	if err != nil {
		return nil, err
	}
	response := &agentpublicv1.ListLibraryFilesResponse{}
	for _, file := range files {
		if file.Position.Kind == domain.FileKindArtifact && strings.Contains(file.Position.Name, request.Msg.GetNameQuery()) {
			response.Files = append(response.Files, &agentpublicv1.LibraryFile{WorkId: l.workID, Name: file.Position.Name})
		}
	}
	return connect.NewResponse(response), nil
}

func (l libraryStandIn) CreateWorkFileAccessLink(ctx context.Context, request *connect.Request[agentpublicv1.CreateWorkFileAccessLinkRequest]) (*connect.Response[agentpublicv1.CreateWorkFileAccessLinkResponse], error) {
	if request.Msg.GetWorkId() != l.workID || request.Msg.GetKind() != agentpublicv1.WorkFileKind_WORK_FILE_KIND_ARTIFACT {
		return nil, connect.NewError(connect.CodeNotFound, nil)
	}
	download := request.Msg.GetDisposition() == agentpublicv1.WorkFileAccessDisposition_WORK_FILE_ACCESS_DISPOSITION_DOWNLOAD
	link, err := l.app.IssueAccessLink(ctx, l.workID, domain.FileKindArtifact, request.Msg.GetName(), download)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&agentpublicv1.CreateWorkFileAccessLinkResponse{AccessUrl: link.URL, AccessPath: link.Path}), nil
}
