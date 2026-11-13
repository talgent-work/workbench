//go:build integration

package storage

import (
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/storage/application"
	"github.com/talgent-work/workbench/internal/pkg/authn"
)

// entry serves Storage's Artifact commands as a Work's Agent calls them
// through the entry: the identity headers the entry writes for its agent
// Identity Token, and the Space Token its credential holder adds.
func (f *fixture) entry() func(workID, spaceToken, procedure, body string) (int, map[string]any) {
	t := f.t
	business := http.NewServeMux()
	mountArtifactService(business, f.app, slog.Default())
	api := httptest.NewServer(business)
	t.Cleanup(api.Close)
	return func(workID, spaceToken, procedure, body string) (int, map[string]any) {
		request, err := http.NewRequest(http.MethodPost, api.URL+procedure, strings.NewReader(body))
		require.NoError(t, err)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set(authn.HeaderAuthenticatedBy, "envoy")
		request.Header.Set(authn.HeaderPrincipal, "agent")
		request.Header.Set(authn.HeaderSubject, workID)
		request.Header.Set(authn.HeaderWorkCaller, "person:owner")
		request.Header.Set(authn.HeaderAgentID, "agent")
		request.Header.Set(authn.HeaderPurpose, "work")
		request.Header.Set(authn.HeaderSpaceToken, spaceToken)
		response, err := api.Client().Do(request)
		require.NoError(t, err)
		defer response.Body.Close()
		var decoded map[string]any
		require.NoError(t, json.NewDecoder(response.Body).Decode(&decoded))
		return response.StatusCode, decoded
	}
}

const createArtifact = "/storage.public.v1.ArtifactService/CreateArtifact"

// A Work's Agent publishes from a Work Directory of its own Work that it
// names; a Directory mounted in it is no source, and no Work reads another's.
// A Work that names none sends the content, within its bound.
func TestAWorkPublishesFromItsOwnWorkDirectory(t *testing.T) {
	f := newFixtureWith(t, func(config *Config) { config.sentContentBytes = 8 })
	artifacts := named("artifacts", "subject", "case:1")
	token := f.open(fileSpace(artifacts))
	f.upload(caseInputs, f.open(fileSpace(caseInputs)), "brief.md", "b", false)
	const work, other = "0193a7c2-6f1e-7c2a-9b3d-5e8f1a2b3c4d", "0193a7c2-6f1e-7c2a-9b3d-5e8f1a2b3c4e"
	f.workspace(work, map[string]application.Located{"inputs": fileSpace(caseInputs)})
	f.workspaceFile(work, "dist/report.md", "# Report")
	call := f.entry()

	status, published := call(work, token, createArtifact, `{"name":"report.md","work_directory":"workspace","path":"dist/report.md"}`)
	require.Equal(t, http.StatusOK, status, published)
	require.Equal(t, "report.md", published["artifact"].(map[string]any)["name"])
	require.Equal(t, work, f.files(artifacts)["report.md"].Attributes.WorkID)
	status, _ = call(work, token, createArtifact, `{"name":"brief.md","work_directory":"workspace","path":"inputs/brief.md"}`)
	require.Equal(t, http.StatusNotFound, status, "a mounted Directory is no source")
	status, refused := call(other, token, createArtifact, `{"name":"stolen.md","work_directory":"workspace","path":"dist/report.md"}`)
	require.NotEqual(t, http.StatusOK, status)
	require.Equal(t, "failed_precondition", refused["code"], "a Work reads no other Work's Work Directory: its own is not open")
	status, _ = call(work, token, createArtifact, `{"name":"both.md","work_directory":"workspace","path":"dist/report.md","content":"eA=="}`)
	require.Equal(t, http.StatusBadRequest, status, "a source is a file or sent content, not both")

	status, published = call(other, token, createArtifact, `{"name":"sent.md","content":"`+base64.StdEncoding.EncodeToString([]byte("sent"))+`"}`)
	require.Equal(t, http.StatusOK, status, published)
	_, body, _ := f.get(mustAccessLink(t, f, at(artifacts, "sent.md")))
	require.Equal(t, "sent", body)
	status, _ = call(other, token, createArtifact, `{"name":"big.md","content":"`+base64.StdEncoding.EncodeToString([]byte("too long!"))+`"}`)
	require.NotEqual(t, http.StatusOK, status, "sent content is bounded")
	require.NotContains(t, f.files(artifacts), "big.md")
	require.NotContains(t, f.files(artifacts), "stolen.md")
}

func mustAccessLink(t *testing.T, f *fixture, position application.Position) string {
	t.Helper()
	link, err := f.app.IssueAccessLink(t.Context(), position, true)
	require.NoError(t, err)
	return link.URL
}
