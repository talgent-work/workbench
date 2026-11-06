package connectrpc

import (
	"context"
	"net/http"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/storage/domain"
	"github.com/talgent-work/workbench/pkg/authn"
)

// Only a verified Work Agent with a workspace publishes Artifacts, from its
// own workspace into the File Space its Space Token names; nothing about
// them comes from the request.
func TestArtifactCommandsTakeTheWorkAgentFromItsVerifiedIdentity(t *testing.T) {
	header := http.Header{}
	header.Set(authn.HeaderSpaceToken, " space ")
	work := authn.NativeProcess{WorkID: "work-1", Purpose: "work", WorkspaceReference: "ws-1"}

	source, err := workAgent(authn.WithNativeProcess(context.Background(), work), header, true)
	require.NoError(t, err)
	require.Equal(t, "work-1", source.WorkID)
	require.Equal(t, "ws-1", source.WorkspaceReference)
	require.Equal(t, "space", source.SpaceToken)

	pm := work
	pm.Purpose, pm.WorkspaceReference = "project_coordination", ""
	for name, ctx := range map[string]context.Context{
		"no verified identity":         context.Background(),
		"a coordinating Work":          authn.WithNativeProcess(context.Background(), pm),
		"a Work without its workspace": authn.WithNativeProcess(context.Background(), authn.NativeProcess{WorkID: "work-1", Purpose: "work"}),
	} {
		_, err := workAgent(ctx, header, true)
		require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err), name)
	}
}

// Content a Work sent needs no Session Workspace; a workspace file does.
// The Work's purpose does not decide it.
func TestSentContentNeedsNoWorkspace(t *testing.T) {
	header := http.Header{}
	header.Set(authn.HeaderSpaceToken, "space")
	managed := authn.WithNativeProcess(context.Background(), authn.NativeProcess{WorkID: "work-1", Purpose: "project_coordination"})

	source, err := publishingAgent(managed, header, "", []byte{})
	require.NoError(t, err)
	require.Equal(t, "work-1", source.WorkID)
	require.Equal(t, "space", source.SpaceToken)
	require.Empty(t, source.WorkspaceReference)
	require.NotNil(t, source.Content, "an empty file sent stays content")

	_, err = publishingAgent(managed, header, "out.md", nil)
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	_, err = publishingAgent(context.Background(), header, "", []byte("x"))
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
}

// A Work with a Session Workspace that sends content is refused as an
// invalid source: it publishes only a file of its workspace.
func TestSentContentFromAWorkWithAWorkspaceIsAnInvalidSource(t *testing.T) {
	header := http.Header{}
	header.Set(authn.HeaderSpaceToken, "space")
	cloud := authn.WithNativeProcess(context.Background(), authn.NativeProcess{WorkID: "work-1", Purpose: "work", WorkspaceReference: "ws-1"})

	source, err := publishingAgent(cloud, header, "", []byte("x"))
	require.NoError(t, err)
	require.Equal(t, "ws-1", source.WorkspaceReference, "the verified workspace travels with the content")
	err = mapError(domain.ErrSentContentWithWorkspace)
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}
