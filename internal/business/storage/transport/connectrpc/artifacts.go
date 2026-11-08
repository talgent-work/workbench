package connectrpc

import (
	"context"
	"strings"

	"connectrpc.com/connect/v2"

	storageprivatev1 "github.com/talgent-work/contracts/gen/go/storage/private/v1"
	"github.com/talgent-work/contracts/gen/go/storage/private/v1/storageprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/storage/application"
	"github.com/talgent-work/workbench/internal/pkg/authn"
)

// ArtifactHandler serves the Storage Agent Command Contract to Work Agents.
// It is reached only through the native API, which verified the Work
// credential the identity below comes from.
type ArtifactHandler struct {
	application *application.Application
}

var _ storageprivatev1connect.ArtifactServiceHandler = (*ArtifactHandler)(nil)

func NewArtifactHandler(app *application.Application) *ArtifactHandler {
	return &ArtifactHandler{application: app}
}

// workAgent is the verified Work Agent as an Artifact source: its Work, its
// Session Workspace when it needs one, and the Space Token it presents,
// which names the File Space it publishes into.
func workAgent(ctx context.Context, header *connect.Header, withWorkspace bool) (application.ArtifactSource, error) {
	process, ok := authn.NativeProcessFromContext(ctx)
	if !ok || process.WorkID == "" || withWorkspace && process.Workspace == nil {
		return application.ArtifactSource{}, connect.NewError(connect.CodePermissionDenied, "storage: only a Work Agent changes Artifacts, and only one with a Session Workspace publishes from it")
	}
	return application.ArtifactSource{
		SpaceToken: strings.TrimSpace(header.Get(authn.HeaderSpaceToken)),
		WorkID:     process.WorkID, Workspace: layoutFromIdentity(process.Workspace),
	}, nil
}

// publishingAgent is the verified Work Agent as the source of a publication:
// a file of its Session Workspace, or the content it sent. The Application
// admits sent content only from a Work without a Session Workspace.
func publishingAgent(ctx context.Context, header *connect.Header, path string, content []byte) (application.ArtifactSource, error) {
	source, err := workAgent(ctx, header, content == nil)
	source.Path, source.Content = path, content
	return source, err
}

func (h *ArtifactHandler) CreateArtifact(ctx context.Context, request *storageprivatev1.CreateArtifactRequest) (*storageprivatev1.CreateArtifactResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	source, err := publishingAgent(ctx, info.RequestHeader(), request.GetPath(), request.Content)
	if err != nil {
		return nil, err
	}
	artifact, err := h.application.PublishArtifact(ctx, source, request.GetName(), false)
	if err != nil {
		return nil, mapError(err)
	}
	return &storageprivatev1.CreateArtifactResponse{Outcome: &storageprivatev1.ArtifactOutcome{Artifact: directoryFileToProto(artifact)}}, nil
}

func (h *ArtifactHandler) ReplaceArtifact(ctx context.Context, request *storageprivatev1.ReplaceArtifactRequest) (*storageprivatev1.ReplaceArtifactResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	source, err := publishingAgent(ctx, info.RequestHeader(), request.GetPath(), request.Content)
	if err != nil {
		return nil, err
	}
	artifact, err := h.application.PublishArtifact(ctx, source, request.GetName(), true)
	if err != nil {
		return nil, mapError(err)
	}
	return &storageprivatev1.ReplaceArtifactResponse{Outcome: &storageprivatev1.ArtifactOutcome{Artifact: directoryFileToProto(artifact)}}, nil
}

func (h *ArtifactHandler) DeleteArtifact(ctx context.Context, request *storageprivatev1.DeleteArtifactRequest) (*storageprivatev1.DeleteArtifactResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	// Deleting needs only the Space Token, never the Work's workspace.
	source, err := workAgent(ctx, info.RequestHeader(), false)
	if err != nil {
		return nil, err
	}
	if err := h.application.DeleteArtifact(ctx, source.SpaceToken, request.GetName(), source.WorkID); err != nil {
		return nil, mapError(err)
	}
	return &storageprivatev1.DeleteArtifactResponse{}, nil
}
