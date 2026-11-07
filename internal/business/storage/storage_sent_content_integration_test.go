//go:build integration

package storage

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	storageprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/storage/private/v1"
	"github.com/talgent-work/workbench/gen/go/talgent/storage/private/v1/storageprivatev1connect"
	"github.com/talgent-work/workbench/internal/pkg/authn"
	"github.com/talgent-work/workbench/internal/pkg/storagepath"
)

// sentContentBound stands in for the 64 MiB bound, which costs seconds to
// send and store; TestTheSentContentBoundIs64MiB pins the production bound.
const sentContentBound = 64 << 10

// A Work on a Managed Computer has no Session Workspace: it publishes and
// replaces an Artifact with the content it sent, up to the bound, through the
// Artifact commands, whose message admits content at the bound; larger
// content is refused before anything is written. It deletes its Artifact
// with its Space Token alone.
func TestAWorkWithoutAWorkspacePublishesTheContentItSent(t *testing.T) {
	f := newFixtureWith(t, func(config *Config) { config.sentContentBytes = sentContentBound })
	space := f.open("case-1")
	mux := http.NewServeMux()
	mux.Handle(artifactServiceHandler(f.app))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The native API verified the Work's credential: a Work without a
		// workspace.
		process := authn.NativeProcess{WorkID: "work-1", Purpose: "work", SubjectID: "case-1"}
		mux.ServeHTTP(w, r.WithContext(authn.WithNativeProcess(r.Context(), process)))
	}))
	t.Cleanup(server.Close)
	client := storageprivatev1connect.NewArtifactServiceClient(server.Client(), server.URL)
	request := func(message *storageprivatev1.CreateArtifactRequest) *connect.Request[storageprivatev1.CreateArtifactRequest] {
		r := connect.NewRequest(message)
		r.Header().Set(authn.HeaderSpaceToken, space.SpaceToken)
		return r
	}
	stored := func() []byte {
		data, err := os.ReadFile(filepath.Join(f.persistent, "assets/spaces", storagepath.Artifacts("case-1"), "report.bin"))
		require.NoError(t, err)
		return data
	}

	_, err := client.CreateArtifact(t.Context(), request(&storageprivatev1.CreateArtifactRequest{Name: "report.bin", Path: "dist/report.bin"}))
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err), "a workspace file needs the Work's workspace")

	largest := bytes.Repeat([]byte{7}, sentContentBound)
	created, err := client.CreateArtifact(t.Context(), request(&storageprivatev1.CreateArtifactRequest{Name: "report.bin", Content: largest}))
	require.NoError(t, err)
	require.Equal(t, int64(sentContentBound), created.Msg.GetOutcome().GetArtifact().GetSizeBytes())
	require.Equal(t, largest, stored())

	replace := connect.NewRequest(&storageprivatev1.ReplaceArtifactRequest{Name: "report.bin", Content: []byte("final")})
	replace.Header().Set(authn.HeaderSpaceToken, space.SpaceToken)
	_, err = client.ReplaceArtifact(t.Context(), replace)
	require.NoError(t, err)
	require.Equal(t, []byte("final"), stored())

	_, err = client.CreateArtifact(t.Context(), request(&storageprivatev1.CreateArtifactRequest{Name: "big.bin", Content: append(largest, 0)}))
	require.Error(t, err, "content above the bound is refused")
	require.NotContains(t, f.files("case-1"), "artifact/big.bin")

	deletion := connect.NewRequest(&storageprivatev1.DeleteArtifactRequest{Name: "report.bin"})
	deletion.Header().Set(authn.HeaderSpaceToken, space.SpaceToken)
	_, err = client.DeleteArtifact(t.Context(), deletion)
	require.NoError(t, err)
	require.NotContains(t, f.files("case-1"), "artifact/report.bin")
}
