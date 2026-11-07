//go:build integration

package storage

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	agentmodule "github.com/talgent-work/workbench/internal/business/agent"
	storagehttp "github.com/talgent-work/workbench/internal/business/storage/transport/http"
	"github.com/talgent-work/workbench/internal/pkg/authn"
	"github.com/talgent-work/workbench/internal/pkg/nativeapi"
	"github.com/talgent-work/workbench/internal/pkg/nativeauth"
)

// Without a platform Connector, a Work's Agent calls the controlplane's
// native API itself, as the core runtime plugin's using-platform-api skill
// teaches: plain Connect JSON over HTTP, with the headers its sandbox's
// Credential Proxy adds. It publishes an Artifact from its workspace, finds
// it in its owner's Personal Library and downloads it through the Access
// Link it is issued. Agent verifies the Work credential and serves the
// Library with its own handlers and rules; Storage keeps the files. The
// Agent reads its owner's Library only: another person's Artifacts stay out
// of it, and the Agent of a Work with a Project reads no one's.
func TestAWorkPublishesAndDownloadsThroughTheNativeAPIWithoutAGateway(t *testing.T) {
	f := newFixture(t)
	agents := newLibraryAgents(t, f)
	own := agents.start("owner", "owner-workspace", "")
	other := agents.start("stranger", "stranger-workspace", "")
	project := agents.start("owner", "project-workspace", "project-1")

	agentMux := http.NewServeMux()
	agentmodule.RegisterRPCHandlers(agentMux, agents.app, agentmodule.RPCConfig{ControlToken: "control", FeedbackToken: "feedback"}, slog.Default(), nil)
	agentServer := httptest.NewServer(agentMux)
	t.Cleanup(agentServer.Close)
	live, err := nativeauth.NewLiveWorkClient(agentServer.Client(), agentServer.URL, "control")
	require.NoError(t, err)

	business := http.NewServeMux()
	business.Handle(artifactServiceHandler(f.app))
	storagehttp.Register(business, f.app, fixtureMaxUploadBytes)
	agentmodule.RegisterPersonalWorkHandlers(business, agents.personalWorks(), live, slog.Default())
	native, err := nativeapi.NewHandler(business, "", live, nativeapi.Calls{SpaceTokenProcedures: AgentArtifactProcedures},
		nativeapi.ReadProcedures{Procedures: agentmodule.AgentLibraryProcedures, Verifier: live})
	require.NoError(t, err)
	api := httptest.NewServer(native)
	t.Cleanup(api.Close)

	// What the Credential Proxy adds to every request to the native API.
	injected := func(work libraryWork) map[string]string {
		return map[string]string{authn.HeaderNativeWorkID: work.id, authn.HeaderNativeCredential: "credential", authn.HeaderSpaceToken: work.space.SpaceToken}
	}
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
	const listLibrary = "/talgent.agent.public.v1.PersonalWorkService/ListLibraryFiles"
	const createAccessLink = "/talgent.agent.public.v1.WorkFileService/CreateWorkFileAccessLink"
	accessLinkTo := func(work libraryWork, name string) string {
		return `{"workId":"` + work.id + `","kind":"WORK_FILE_KIND_ARTIFACT","name":"` + name + `","disposition":"WORK_FILE_ACCESS_DISPOSITION_DOWNLOAD"}`
	}

	f.workspaceFile(own.workspace, "dist/report.md", "# Report")
	status, published := call("/talgent.storage.private.v1.ArtifactService/CreateArtifact", `{"name":"report.md","path":"dist/report.md"}`, injected(own))
	require.Equal(t, http.StatusOK, status, published)
	require.Equal(t, "report.md", published["outcome"].(map[string]any)["artifact"].(map[string]any)["name"])
	require.Contains(t, f.files(own.id), "artifact/report.md")
	status, refused := call("/talgent.storage.private.v1.ArtifactService/CreateArtifact", `{"name":"report.md","path":"dist/report.md"}`, injected(own))
	require.Equal(t, http.StatusConflict, status, "an existing name is refused: replace it on purpose")
	require.Equal(t, "already_exists", refused["code"])
	status, _ = call("/talgent.storage.private.v1.ArtifactService/CreateArtifact", `{"name":"other.md","path":"dist/report.md"}`, map[string]string{authn.HeaderNativeWorkID: own.id, authn.HeaderNativeCredential: "guess"})
	require.Equal(t, http.StatusUnauthorized, status, "only the Work's current credential is admitted")

	// Another person's Agent publishes into that person's own Personal Work.
	f.workspaceFile(other.workspace, "dist/report-secret.md", "# Secret")
	status, published = call("/talgent.storage.private.v1.ArtifactService/CreateArtifact", `{"name":"report-secret.md","path":"dist/report-secret.md"}`, injected(other))
	require.Equal(t, http.StatusOK, status, published)

	status, found := call(listLibrary, `{"nameQuery":"report"}`, injected(own))
	require.Equal(t, http.StatusOK, status, found)
	files := found["files"].([]any)
	require.Len(t, files, 1, "the owner's Library holds only the owner's Artifacts")
	file := files[0].(map[string]any)
	require.Equal(t, map[string]any{"workId": own.id, "name": "report.md"}, map[string]any{"workId": file["workId"], "name": file["name"]})
	status, issued := call(createAccessLink, accessLinkTo(own, "report.md"), injected(own))
	require.Equal(t, http.StatusOK, status, issued)
	accessPath := issued["accessPath"].(string)
	require.True(t, strings.HasPrefix(accessPath, nativeapi.AccessLinkPath+"?"), accessPath)

	// The Library holds Artifacts: the Work's own Attachment is not read
	// through it.
	require.Equal(t, http.StatusOK, f.upload(own.id, own.space.SpaceToken, "brief.md", "# Brief", false).StatusCode)
	status, refused = call(createAccessLink, `{"workId":"`+own.id+`","kind":"WORK_FILE_KIND_ATTACHMENT","name":"brief.md","disposition":"WORK_FILE_ACCESS_DISPOSITION_DOWNLOAD"}`, injected(own))
	require.Equal(t, http.StatusNotFound, status, refused)

	status, refused = call(createAccessLink, accessLinkTo(other, "report-secret.md"), injected(own))
	require.Equal(t, http.StatusForbidden, status, refused)
	require.Equal(t, "permission_denied", refused["code"], "another person's Work is not the owner's to read")

	status, refused = call(listLibrary, `{}`, injected(project))
	require.Equal(t, http.StatusForbidden, status, refused)
	require.Equal(t, "permission_denied", refused["code"], "the Agent of a Work with a Project reads no one's Library")

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
	status, content := download(map[string]string{authn.HeaderNativeWorkID: own.id, authn.HeaderNativeCredential: "credential"})
	require.Equal(t, http.StatusOK, status, content)
	require.Equal(t, "# Report", content)
	status, _ = download(nil)
	require.Equal(t, http.StatusUnauthorized, status, "a link is read only with the Work's credential")
}
