//go:build integration

package storage

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/stretchr/testify/require"

	agentprivatev1 "github.com/talgent-work/contracts/gen/go/agent/private/v1"
	"github.com/talgent-work/contracts/gen/go/agent/private/v1/agentprivatev1connect"
	storageprivatev1 "github.com/talgent-work/contracts/gen/go/storage/private/v1"
	"github.com/talgent-work/workbench/internal/business/storage/application"
	"github.com/talgent-work/workbench/internal/pkg/authn"
	"github.com/talgent-work/workbench/internal/pkg/nativeapi"
	"github.com/talgent-work/workbench/internal/pkg/nativeauth"
	"github.com/talgent-work/workbench/internal/testsupport/connecttest"
)

// verifyingAgent stands in for Agent: it verifies each Work's credential
// and answers its Session Workspace, if it has one.
type verifyingAgent struct {
	agentprivatev1connect.UnimplementedAgentServiceHandler
	workspaces map[string]*storageprivatev1.WorkspaceLayout
}

func (a *verifyingAgent) VerifyWorkCredential(_ context.Context, r *agentprivatev1.VerifyWorkCredentialRequest) (*agentprivatev1.VerifyWorkCredentialResponse, error) {
	workspace, known := a.workspaces[r.GetWorkId()]
	if !known || r.GetCredential() != "credential" {
		return nil, connect.NewError(connect.CodePermissionDenied, "")
	}
	return &agentprivatev1.VerifyWorkCredentialResponse{WorkId: r.GetWorkId(), SubjectId: "owner", AgentId: "agent", Purpose: "work", Workspace: workspace}, nil
}

// nativeAPI serves Storage's Artifact commands as a Work's Agent calls
// them, with the headers its Credential Proxy adds.
func (f *fixture) nativeAPI(agent *verifyingAgent) func(workID, spaceToken, procedure, body string) (int, map[string]any) {
	t := f.t
	agentServer := httptest.NewServer(connecttest.Handler(func(s *connect.Server) { agentprivatev1connect.RegisterAgentServiceHandler(s, agent) }))
	t.Cleanup(agentServer.Close)
	live, err := nativeauth.NewLiveWorkClient(agentServer.Client(), agentServer.URL, "control")
	require.NoError(t, err)
	business := http.NewServeMux()
	mountArtifactService(business, f.app, slog.Default())
	native, err := nativeapi.NewHandler(business, "", live, nativeapi.Calls{SpaceTokenProcedures: AgentArtifactProcedures}, nativeapi.ReadProcedures{Verifier: live})
	require.NoError(t, err)
	api := httptest.NewServer(native)
	t.Cleanup(api.Close)
	return func(workID, spaceToken, procedure, body string) (int, map[string]any) {
		request, err := http.NewRequest(http.MethodPost, api.URL+procedure, strings.NewReader(body))
		require.NoError(t, err)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set(authn.HeaderNativeWorkID, workID)
		request.Header.Set(authn.HeaderNativeCredential, "credential")
		request.Header.Set(authn.HeaderSpaceToken, spaceToken)
		response, err := api.Client().Do(request)
		require.NoError(t, err)
		defer response.Body.Close()
		var decoded map[string]any
		require.NoError(t, json.NewDecoder(response.Body).Decode(&decoded))
		return response.StatusCode, decoded
	}
}

const createArtifact = "/storage.private.v1.ArtifactService/CreateArtifact"

// layoutMessage is a layout as Agent answers it.
func layoutMessage(layout application.Layout) *storageprivatev1.WorkspaceLayout {
	directory := func(named application.Named) *storageprivatev1.Directory {
		owner := &storageprivatev1.DirectoryOwner{}
		for _, fact := range named.Owner {
			owner.Facts = append(owner.Facts, &storageprivatev1.OwnerFact{Condition: fact.Condition, Value: fact.Value})
		}
		return &storageprivatev1.Directory{Owner: owner, Name: named.Name}
	}
	message := &storageprivatev1.WorkspaceLayout{Workspace: directory(layout.Workspace)}
	for _, mount := range layout.Mounts {
		message.Mounts = append(message.Mounts, &storageprivatev1.MountedDirectory{Path: mount.Path, Directory: directory(mount.Directory)})
	}
	return message
}

// A Work's Agent publishes from the Session Workspace its verified identity
// carries: the path is read in that layout, and a Directory mounted in it
// is no source. A Work without one sends the content, within its bound.
func TestAWorkPublishesFromTheWorkspaceItsIdentityCarries(t *testing.T) {
	f := newFixtureWith(t, func(config *Config) { config.sentContentBytes = 8 })
	artifacts := named("artifacts", "subject", "case:1")
	token := f.open(fileSpace(artifacts))
	f.upload(caseInputs, f.open(fileSpace(caseInputs)), "brief.md", "b", false)
	layout := f.workspace("work-1", map[string]application.Located{"inputs": fileSpace(caseInputs)})
	f.workspaceFile("work-1", "dist/report.md", "# Report")
	call := f.nativeAPI(&verifyingAgent{workspaces: map[string]*storageprivatev1.WorkspaceLayout{"work-1": layoutMessage(layout), "cloudless": nil}})

	status, published := call("work-1", token, createArtifact, `{"name":"report.md","path":"dist/report.md"}`)
	require.Equal(t, http.StatusOK, status, published)
	require.Equal(t, "work-1", f.files(artifacts)["report.md"].Attributes.WorkID)
	status, _ = call("work-1", token, createArtifact, `{"name":"brief.md","path":"inputs/brief.md"}`)
	require.Equal(t, http.StatusBadRequest, status, "a mounted Directory is no source")
	status, _ = call("work-1", token, createArtifact, `{"name":"sent.md","content":"`+base64.StdEncoding.EncodeToString([]byte("x"))+`"}`)
	require.Equal(t, http.StatusBadRequest, status, "a Work with a Session Workspace publishes from it")

	status, published = call("cloudless", token, createArtifact, `{"name":"sent.md","content":"`+base64.StdEncoding.EncodeToString([]byte("sent"))+`"}`)
	require.Equal(t, http.StatusOK, status, published)
	_, body, _ := f.get(mustAccessLink(t, f, at(artifacts, "sent.md")))
	require.Equal(t, "sent", body)
	status, _ = call("cloudless", token, createArtifact, `{"name":"big.md","content":"`+base64.StdEncoding.EncodeToString([]byte("too long!"))+`"}`)
	require.NotEqual(t, http.StatusOK, status, "sent content is bounded")
	status, _ = call("cloudless", token, createArtifact, `{"name":"path.md","path":"dist/report.md"}`)
	require.Equal(t, http.StatusForbidden, status, "a Work without a Session Workspace has no path to read")
	require.NotContains(t, f.files(artifacts), "big.md")
}

func mustAccessLink(t *testing.T, f *fixture, position application.Position) string {
	t.Helper()
	link, err := f.app.IssueAccessLink(t.Context(), position, true)
	require.NoError(t, err)
	return link.URL
}
