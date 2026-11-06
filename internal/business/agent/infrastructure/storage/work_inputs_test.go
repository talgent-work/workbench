package storage

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	storageprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/storage/private/v1"
	"github.com/talgent-work/workbench/gen/go/talgent/storage/private/v1/storageprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure/execution"
)

// fileSpace is Storage's File Space Contract over one owner's space, whose
// Artifact and vanished Attachment are not inputs.
type fileSpace struct {
	storageprivatev1connect.UnimplementedFileSpaceServiceHandler
	tokens []string
}

var updated = time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)

func (f *fileSpace) ResolveInputSources(_ context.Context, r *connect.Request[storageprivatev1.ResolveInputSourcesRequest]) (*connect.Response[storageprivatev1.ResolveInputSourcesResponse], error) {
	f.tokens = append(f.tokens, r.Header().Get("Authorization"))
	resolved := make([]*storageprivatev1.ResolvedInputSource, 0, len(r.Msg.GetInputSources()))
	for _, source := range r.Msg.GetInputSources() {
		owner := ""
		if source == "file-space/case-1" {
			owner = "case-1"
		}
		resolved = append(resolved, &storageprivatev1.ResolvedInputSource{InputSource: source, Owner: owner})
	}
	return connect.NewResponse(&storageprivatev1.ResolveInputSourcesResponse{InputSources: resolved}), nil
}

func (f *fileSpace) ListFiles(_ context.Context, r *connect.Request[storageprivatev1.ListFilesRequest]) (*connect.Response[storageprivatev1.ListFilesResponse], error) {
	if r.Msg.GetOwner() != "case-1" {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("no such space"))
	}
	return connect.NewResponse(&storageprivatev1.ListFilesResponse{Files: []*storageprivatev1.File{
		{Kind: storageprivatev1.FileKind_FILE_KIND_ATTACHMENT, Name: "brief.pdf", SizeBytes: 1024, UpdatedAt: timestamppb.New(updated)},
		{Kind: storageprivatev1.FileKind_FILE_KIND_ARTIFACT, Name: "report.md", SizeBytes: 3},
		{Kind: storageprivatev1.FileKind_FILE_KIND_ATTACHMENT, Name: "gone.csv", SizeBytes: 2},
	}}), nil
}

func (f *fileSpace) IssueAccessLink(_ context.Context, r *connect.Request[storageprivatev1.IssueAccessLinkRequest]) (*connect.Response[storageprivatev1.IssueAccessLinkResponse], error) {
	if r.Msg.GetName() == "gone.csv" {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("gone"))
	}
	if r.Msg.GetDisposition() != storageprivatev1.AccessDisposition_ACCESS_DISPOSITION_DOWNLOAD || r.Msg.GetKind() != storageprivatev1.FileKind_FILE_KIND_ATTACHMENT {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("an input is downloaded as an Attachment"))
	}
	return connect.NewResponse(&storageprivatev1.IssueAccessLinkResponse{AccessPath: "/v1/storage/content?grant=" + r.Msg.GetName()}), nil
}

// A Work's inputs are its input source's owner and that space's current
// Attachments, each with a download link; one deleted since listed is left
// for the next sync.
func TestWorkInputsAreTheAttachmentsOfTheInputSourcesSpace(t *testing.T) {
	space := &fileSpace{}
	mux := http.NewServeMux()
	mux.Handle(storageprivatev1connect.NewFileSpaceServiceHandler(space))
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
