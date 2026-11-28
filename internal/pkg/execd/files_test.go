package execd_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/pkg/execd"
)

func TestUploadWritesTheFileWithItsModeThroughTheFileAPI(t *testing.T) {
	var metadata struct {
		Path string `json:"path"`
		Mode int    `json:"mode"`
	}
	var content []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/files/upload", r.URL.Path)
		require.Equal(t, "test-only", r.Header.Get("X-EXECD-ACCESS-TOKEN"))
		require.NoError(t, r.ParseMultipartForm(1<<20))
		meta, _, err := r.FormFile("metadata")
		require.NoError(t, err)
		require.NoError(t, json.NewDecoder(meta).Decode(&metadata))
		file, _, err := r.FormFile("file")
		require.NoError(t, err)
		content, err = io.ReadAll(file)
		require.NoError(t, err)
	}))
	defer server.Close()
	client, err := execd.NewClient(server.Client(), server.URL, "test-only")
	require.NoError(t, err)
	require.NoError(t, client.Upload(context.Background(), "/tmp/run-file", 0o600, []byte("A=b\x00")))
	require.Equal(t, "/tmp/run-file", metadata.Path)
	require.Equal(t, 600, metadata.Mode)
	require.Equal(t, []byte("A=b\x00"), content)
}

func TestFailedUploadDoesNotReportTheContentOrTheResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("echoed private-value"))
	}))
	defer server.Close()
	client, err := execd.NewClient(server.Client(), server.URL, "test-only")
	require.NoError(t, err)
	err = client.Upload(context.Background(), "/tmp/run-file", 0o600, []byte("TOKEN=private-value\x00"))
	require.ErrorContains(t, err, "HTTP 500")
	require.NotContains(t, err.Error(), "private-value")
	require.Error(t, client.Upload(context.Background(), "relative", 0o600, nil))
}
