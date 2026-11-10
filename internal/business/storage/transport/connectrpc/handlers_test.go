package connectrpc

import (
	"context"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/stretchr/testify/require"

	storageprivatev1 "github.com/talgent-work/contracts/gen/go/storage/private/v1"
	"github.com/talgent-work/workbench/internal/business/storage/application"
	"github.com/talgent-work/workbench/internal/business/storage/domain"
	"github.com/talgent-work/workbench/internal/pkg/authn"
	"github.com/talgent-work/workbench/internal/pkg/storageerror"
)

// agentOf is a request of the agent Principal for work.
func agentOf(work string) context.Context {
	return authn.WithPrincipal(context.Background(), authn.AgentPrincipal(authn.Agent{WorkID: work, AgentID: "agent", Purpose: "work"}))
}

// A Work Agent's Artifact command speaks for its own Work, into the File
// Space its Space Token names; the Work never comes from the request, and a
// person changes no Artifact here.
func TestArtifactCommandsTakeTheWorkFromTheAgentPrincipal(t *testing.T) {
	source, err := publishingAgent(agentOf("work-1"), " space ", " workspace ", "dist/out.md", nil)
	require.NoError(t, err)
	require.Equal(t, application.ArtifactSource{SpaceToken: "space", WorkID: "work-1", WorkDirectory: "workspace", Path: "dist/out.md"}, source)

	sent, err := publishingAgent(agentOf("work-1"), "space", "", "", []byte{})
	require.NoError(t, err)
	require.NotNil(t, sent.Content, "an empty file sent stays content")

	person := authn.WithPrincipal(context.Background(), authn.UserPrincipal(authn.AuthenticatedUser{ID: "owner"}))
	for name, ctx := range map[string]context.Context{"no Principal": context.Background(), "a person": person} {
		_, err := workAgent(ctx, "space")
		require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err), name)
	}
}

// Each Directory refusal reaches its caller with a reason of its own.
func TestDirectoryRefusalsCarryTheirReason(t *testing.T) {
	for err, want := range map[error]storageprivatev1.ErrorReason{
		domain.ErrInvalidLocator:        storageprivatev1.ErrorReason_ERROR_REASON_INVALID_LOCATOR,
		domain.ErrDirectoryNotOpen:      storageprivatev1.ErrorReason_ERROR_REASON_DIRECTORY_NOT_OPEN,
		domain.ErrDirectoryNotAllocated: storageprivatev1.ErrorReason_ERROR_REASON_DIRECTORY_NOT_ALLOCATED,
		domain.ErrDirectoryReclaimed:    storageprivatev1.ErrorReason_ERROR_REASON_DIRECTORY_RECLAIMED,
		domain.ErrPermanentDirectory:    storageprivatev1.ErrorReason_ERROR_REASON_PERMANENT_DIRECTORY,
		application.ErrNameTaken:        storageprivatev1.ErrorReason_ERROR_REASON_NAME_TAKEN,
	} {
		require.Equal(t, want, storageerror.ReasonOf(mapError(err)), err.Error())
	}
}

// An Artifact command refuses in the public contract's vocabulary: a reason
// it names keeps its detail, a private-only one travels without one.
func TestArtifactRefusalsSpeakThePublicContract(t *testing.T) {
	require.Equal(t, storageprivatev1.ErrorReason_ERROR_REASON_NAME_TAKEN, storageerror.ReasonOf(mapArtifactError(application.ErrNameTaken)))
	refused := mapArtifactError(domain.ErrPermanentDirectory)
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(refused))
	require.Equal(t, storageprivatev1.ErrorReason_ERROR_REASON_UNSPECIFIED, storageerror.ReasonOf(refused))
}
