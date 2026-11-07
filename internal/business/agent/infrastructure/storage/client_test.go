package storage

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/stretchr/testify/require"

	storageprivatev1 "github.com/talgent-work/contracts/gen/go/storage/private/v1"
	"github.com/talgent-work/contracts/gen/go/storage/private/v1/storageprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/agent/application"
)

// judge answers as Storage's Preview Policy would for a text file, a binary
// it refuses, and a mode this build does not know.
type judge struct {
	storageprivatev1connect.UnimplementedSandboxMountServiceHandler
	heads []string
}

func (j *judge) JudgeFilePreview(_ context.Context, r *storageprivatev1.JudgeFilePreviewRequest) (*storageprivatev1.JudgeFilePreviewResponse, error) {
	j.heads = append(j.heads, string(r.GetHead()))
	response := &storageprivatev1.JudgeFilePreviewResponse{ReadLimitBytes: 1 << 20}
	switch r.GetName() {
	case "notes.md":
		response.Preview = &storageprivatev1.FilePreview{Mode: storageprivatev1.PreviewMode_PREVIEW_MODE_DIRECT, Display: storageprivatev1.PreviewDisplay_PREVIEW_DISPLAY_TEXT}
	case "data.bin":
		response.Preview = &storageprivatev1.FilePreview{Mode: storageprivatev1.PreviewMode_PREVIEW_MODE_REFUSED, Refusal: storageprivatev1.PreviewRefusal_PREVIEW_REFUSAL_UNSUPPORTED}
	default:
		response.Preview = &storageprivatev1.FilePreview{Mode: storageprivatev1.PreviewMode_PREVIEW_MODE_RENDER, Display: storageprivatev1.PreviewDisplay_PREVIEW_DISPLAY_PDF}
	}
	return response, nil
}

// Storage's verdict on a Managed Work's file reaches Agent with the bytes its
// reader is served; a refused or unknown verdict serves nothing.
func TestStorageJudgesAManagedWorksFilePreview(t *testing.T) {
	storage := &judge{}
	mux := http.NewServeMux()
	srv := connect.NewServer()
	storageprivatev1connect.RegisterSandboxMountServiceHandler(srv, storage)
	connecthttp.Mount(mux, srv, connecthttp.WithReadMaxBytes(0))
	server := httptest.NewServer(mux)
	defer server.Close()
	client, err := NewClient(server.Client(), Config{Address: server.URL, Token: "storage-token"})
	require.NoError(t, err)
	ctx := context.Background()

	preview, served, err := client.JudgeFilePreview(ctx, "notes.md", 5, []byte("# hi\n"))
	require.NoError(t, err)
	require.Equal(t, application.WorkspaceFilePreview{Mode: application.WorkspacePreviewDirect, Display: application.WorkspacePreviewText}, preview)
	require.EqualValues(t, 1<<20, served)
	for _, name := range []string{"data.bin", "slides.pptx"} {
		preview, served, err = client.JudgeFilePreview(ctx, name, 5, []byte("x"))
		require.NoError(t, err)
		require.Equal(t, application.WorkspaceFilePreview{Mode: application.WorkspacePreviewRefused, Refusal: application.WorkspacePreviewUnsupported}, preview, name)
		require.Zero(t, served, name)
	}
	require.Equal(t, []string{"# hi\n", "x", "x"}, storage.heads)
}
