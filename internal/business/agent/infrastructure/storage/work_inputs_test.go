package storage

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	storageprivatev1 "github.com/talgent-work/contracts/gen/storage/private/v1"
	"github.com/talgent-work/contracts/gen/storage/private/v1/storageprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure/execution"
)

// fileSpace is Storage's File Space Contract over one owner's space, whose
// Artifact and vanished Attachment are not inputs.
type fileSpace struct {
	storageprivatev1connect.UnimplementedFileSpaceServiceHandler
	tokens []string
}

var updated = time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)

func (f *fileSpace) ResolveInputSources(ctx context.Context, r *storageprivatev1.ResolveInputSourcesRequest) (*storageprivatev1.ResolveInputSourcesResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	f.tokens = append(f.tokens, info.RequestHeader().Get("Authorization"))
	resolved := make([]*storageprivatev1.ResolvedInputSource, 0, len(r.GetInputSources()))
	for _, source := range r.GetInputSources() {
		owner := ""
		if source == "file-space/case-1" {
			owner = "case-1"
		}
		resolved = append(resolved, &storageprivatev1.ResolvedInputSource{InputSource: source, Owner: owner})
	}
	return &storageprivatev1.ResolveInputSourcesResponse{InputSources: resolved}, nil
}

func (f *fileSpace) ListFiles(_ context.Context, r *storageprivatev1.ListFilesRequest) (*storageprivatev1.ListFilesResponse, error) {
	if r.GetOwner() != "case-1" {
		return nil, connect.NewError(connect.CodeNotFound, "no such space")
	}
	return &storageprivatev1.ListFilesResponse{Files: []*storageprivatev1.File{
		{Kind: storageprivatev1.FileKind_FILE_KIND_ATTACHMENT, Name: "brief.pdf", SizeBytes: 1024, UpdatedAt: timestamppb.New(updated)},
		{Kind: storageprivatev1.FileKind_FILE_KIND_ARTIFACT, Name: "report.md", SizeBytes: 3},
		{Kind: storageprivatev1.FileKind_FILE_KIND_ATTACHMENT, Name: "gone.csv", SizeBytes: 2},
	}}, nil
}

func (f *fileSpace) IssueAccessLink(_ context.Context, r *storageprivatev1.IssueAccessLinkRequest) (*storageprivatev1.IssueAccessLinkResponse, error) {
	if r.GetName() == "gone.csv" {
		return nil, connect.NewError(connect.CodeNotFound, "gone")
	}
	if r.GetDisposition() != storageprivatev1.AccessDisposition_ACCESS_DISPOSITION_DOWNLOAD || r.GetKind() != storageprivatev1.FileKind_FILE_KIND_ATTACHMENT {
		return nil, connect.NewError(connect.CodeInvalidArgument, "an input is downloaded as an Attachment")
	}
	return &storageprivatev1.IssueAccessLinkResponse{AccessPath: "/v1/storage/content?grant=" + r.GetName()}, nil
}

// A Work's inputs are its input source's owner and that space's current
// Attachments, each with a download link; one deleted since listed is left
// for the next sync.
func TestWorkInputsAreTheAttachmentsOfTheInputSourcesSpace(t *testing.T) {
	space := &fileSpace{}
	mux := http.NewServeMux()
	srv := connect.NewServer()
	storageprivatev1connect.RegisterFileSpaceServiceHandler(srv, space)
	connecthttp.Mount(mux, srv, connecthttp.WithReadMaxBytes(0))
	server := httptest.NewServer(mux)
	defer server.Close()
	inputs, err := NewWorkInputs(server.Client(), Config{Address: server.URL, Token: "storage-token"})
	require.NoError(t, err)
	ctx := context.Background()

	owner, err := inputs.InputOwner(ctx, "file-space/case-1")
	require.NoError(t, err)
	require.Equal(t, "case-1", owner)
	owner, err = inputs.InputOwner(ctx, "file-space/unknown")
	require.NoError(t, err)
	require.Empty(t, owner)
	require.Equal(t, []string{"Bearer storage-token", "Bearer storage-token"}, space.tokens)

	attachments, err := inputs.InputAttachments(ctx, "case-1")
	require.NoError(t, err)
	require.Equal(t, []execution.InputAttachment{{Name: "brief.pdf", SizeBytes: 1024, UpdatedAt: updated, AccessPath: "/v1/storage/content?grant=brief.pdf"}}, attachments)

	_, err = NewWorkInputs(server.Client(), Config{Address: server.URL})
	require.Error(t, err)
}
