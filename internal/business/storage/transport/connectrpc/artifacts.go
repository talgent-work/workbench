package connectrpc

import (
	"context"
	"errors"
	"strings"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/protobuf/types/known/timestamppb"

	storageprivatev1 "github.com/talgent-work/contracts/gen/go/storage/private/v1"
	storagepublicv1 "github.com/talgent-work/contracts/gen/go/storage/public/v1"
	"github.com/talgent-work/contracts/gen/go/storage/public/v1/storagepublicv1connect"
	"github.com/talgent-work/workbench/internal/business/storage/application"
	"github.com/talgent-work/workbench/internal/pkg/authn"
	"github.com/talgent-work/workbench/internal/pkg/storageerror"
)

// ArtifactHandler serves the Storage Agent Command Contract to Work Agents,
// each calling as its Work's agent Principal with its Space Token.
type ArtifactHandler struct {
	application *application.Application
}

var _ storagepublicv1connect.ArtifactServiceHandler = (*ArtifactHandler)(nil)

func NewArtifactHandler(app *application.Application) *ArtifactHandler {
	return &ArtifactHandler{application: app}
}

// workAgent maps the request's Principal to the Artifact source it is: the
// Work of the calling Agent, with the Space Token it presents, which names
// the File Space it writes. Only a Work Agent changes Artifacts.
func workAgent(ctx context.Context, spaceToken string) (application.ArtifactSource, error) {
	agent, ok := authn.CurrentAgent(ctx)
	if !ok || agent.WorkID == "" {
		return application.ArtifactSource{}, connect.NewError(connect.CodePermissionDenied, "storage: only a Work Agent changes Artifacts")
	}
	return application.ArtifactSource{SpaceToken: strings.TrimSpace(spaceToken), WorkID: agent.WorkID}, nil
}

// spaceToken is the Space Token the request presents.
func spaceToken(ctx context.Context) string {
	info, ok := connect.CallInfoForServerContext(ctx)
	if !ok {
		return ""
	}
	return info.RequestHeader().Get(authn.HeaderSpaceToken)
}

// publishingAgent is the Work Agent as the source of a publication: a file
// of the Work Directory it names, or the content it sent.
func publishingAgent(ctx context.Context, token, workDirectory, path string, content []byte) (application.ArtifactSource, error) {
	source, err := workAgent(ctx, token)
	source.WorkDirectory, source.Path, source.Content = strings.TrimSpace(workDirectory), path, content
	return source, err
}

func (h *ArtifactHandler) CreateArtifact(ctx context.Context, request *storagepublicv1.CreateArtifactRequest) (*storagepublicv1.CreateArtifactResponse, error) {
	source, err := publishingAgent(ctx, spaceToken(ctx), request.GetWorkDirectory(), request.GetPath(), request.Content)
	if err != nil {
		return nil, err
	}
	artifact, err := h.application.PublishArtifact(ctx, source, request.GetName(), false)
	if err != nil {
		return nil, mapArtifactError(err)
	}
	return &storagepublicv1.CreateArtifactResponse{Artifact: artifactToProto(artifact)}, nil
}

func (h *ArtifactHandler) ReplaceArtifact(ctx context.Context, request *storagepublicv1.ReplaceArtifactRequest) (*storagepublicv1.ReplaceArtifactResponse, error) {
	source, err := publishingAgent(ctx, spaceToken(ctx), request.GetWorkDirectory(), request.GetPath(), request.Content)
	if err != nil {
		return nil, err
	}
	artifact, err := h.application.PublishArtifact(ctx, source, request.GetName(), true)
	if err != nil {
		return nil, mapArtifactError(err)
	}
	return &storagepublicv1.ReplaceArtifactResponse{Artifact: artifactToProto(artifact)}, nil
}

func (h *ArtifactHandler) DeleteArtifact(ctx context.Context, request *storagepublicv1.DeleteArtifactRequest) (*storagepublicv1.DeleteArtifactResponse, error) {
	source, err := workAgent(ctx, spaceToken(ctx))
	if err != nil {
		return nil, err
	}
	if err := h.application.DeleteArtifact(ctx, source.SpaceToken, request.GetName(), source.WorkID); err != nil {
		return nil, mapArtifactError(err)
	}
	return &storagepublicv1.DeleteArtifactResponse{}, nil
}

// mapArtifactError refuses in the public contract's vocabulary: a reason
// storage.public.v1.ErrorReason names travels as that value, any other none.
func mapArtifactError(err error) error {
	refused := mapError(err)
	reason := storageerror.ReasonOf(refused)
	if reason == storageprivatev1.ErrorReason_ERROR_REASON_UNSPECIFIED {
		return refused
	}
	var connectErr *connect.Error
	if !errors.As(refused, &connectErr) {
		return refused
	}
	public := connect.NewError(connectErr.Code(), connectErr.Message()).WithCause(err)
	if _, named := storagepublicv1.ErrorReason_value[reason.String()]; named {
		detail, detailErr := connectproto.NewErrorDetail(&errdetails.ErrorInfo{Domain: storageerror.Domain, Reason: reason.String()})
		if detailErr == nil {
			public = public.WithDetail(detail)
		}
	}
	return public
}

func artifactToProto(file application.DirectoryFile) *storagepublicv1.Artifact {
	return &storagepublicv1.Artifact{
		Name: file.Position.Name, SizeBytes: file.SizeBytes, ContentType: file.ContentType(), UpdatedAt: timestamppb.New(file.ModifiedAt),
	}
}
