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

var workspaceLayout = &authn.WorkspaceLayout{
	Workspace: authn.Directory{Owner: []authn.OwnerFact{{Condition: "work", Value: "work-1"}}, Name: "workspace"},
	Mounts:    []authn.MountedDirectory{{Path: "inputs", Directory: authn.Directory{Owner: []authn.OwnerFact{{Condition: "subject", Value: "case:1"}}, Name: "inputs"}}},
}

// Only a verified Work Agent with a Session Workspace publishes Artifacts,
// from its own workspace into the File Space its Space Token names; nothing
// about them comes from the request.
func TestArtifactCommandsTakeTheWorkAgentFromItsVerifiedIdentity(t *testing.T) {
	header := new(connect.Header)
	header.Set(authn.HeaderSpaceToken, " space ")
	work := authn.NativeProcess{WorkID: "work-1", Purpose: "work", Workspace: workspaceLayout}

	source, err := workAgent(authn.WithNativeProcess(context.Background(), work), header, true)
	require.NoError(t, err)
	require.Equal(t, "work-1", source.WorkID)
	require.Equal(t, "space", source.SpaceToken)
	require.Equal(t, &application.Layout{
		Workspace: application.Named{Owner: []domain.OwnerFact{{Condition: "work", Value: "work-1"}}, Name: "workspace"},
		Mounts: []application.Mount{{Path: "inputs", Directory: application.Named{
			Owner: []domain.OwnerFact{{Condition: "subject", Value: "case:1"}}, Name: "inputs",
		}}},
	}, source.Workspace)

	for name, ctx := range map[string]context.Context{
		"no verified identity":         context.Background(),
		"a Work without its workspace": authn.WithNativeProcess(context.Background(), authn.NativeProcess{WorkID: "work-1", Purpose: "work"}),
	} {
		_, err := workAgent(ctx, header, true)
		require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err), name)
	}
}

// Content a Work sent needs no Session Workspace; a workspace file does.
func TestSentContentNeedsNoWorkspace(t *testing.T) {
	header := new(connect.Header)
	header.Set(authn.HeaderSpaceToken, "space")
	managed := authn.WithNativeProcess(context.Background(), authn.NativeProcess{WorkID: "work-1", Purpose: "project_coordination"})

	source, err := publishingAgent(managed, header, "", []byte{})
	require.NoError(t, err)
	require.Equal(t, "work-1", source.WorkID)
	require.Nil(t, source.Workspace)
	require.NotNil(t, source.Content, "an empty file sent stays content")

	_, err = publishingAgent(managed, header, "out.md", nil)
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	_, err = publishingAgent(context.Background(), header, "", []byte("x"))
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
}

// Each Directory refusal reaches its caller with a reason of its own.
func TestDirectoryRefusalsCarryTheirReason(t *testing.T) {
	for err, want := range map[error]storageprivatev1.ErrorReason{
		domain.ErrInvalidLocator:           storageprivatev1.ErrorReason_ERROR_REASON_INVALID_LOCATOR,
		domain.ErrDirectoryNotOpen:         storageprivatev1.ErrorReason_ERROR_REASON_DIRECTORY_NOT_OPEN,
		domain.ErrDirectoryNotAllocated:    storageprivatev1.ErrorReason_ERROR_REASON_DIRECTORY_NOT_ALLOCATED,
		domain.ErrDirectoryReclaimed:       storageprivatev1.ErrorReason_ERROR_REASON_DIRECTORY_RECLAIMED,
		domain.ErrPermanentDirectory:       storageprivatev1.ErrorReason_ERROR_REASON_PERMANENT_DIRECTORY,
		domain.ErrSentContentWithWorkspace: storageprivatev1.ErrorReason_ERROR_REASON_INVALID_SOURCE,
		application.ErrNameTaken:           storageprivatev1.ErrorReason_ERROR_REASON_NAME_TAKEN,
	} {
		require.Equal(t, want, storageerror.ReasonOf(mapError(err)), err.Error())
	}
}
