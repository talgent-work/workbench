package workspacefile_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/transport/workspacefile"
)

type files struct {
	opened application.OpenedWorkspaceFile
	err    error
}

func (f files) Open(context.Context, string) (application.OpenedWorkspaceFile, error) {
	return f.opened, f.err
}

func get(t *testing.T, f files) *http.Response {
	t.Helper()
	mux := http.NewServeMux()
	workspacefile.Register(mux, f, slog.New(slog.DiscardHandler))
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, workspacefile.Path+"?grant=g", nil))
	return recorder.Result()
}

// A download is served under its name as an attachment; a shown file is
// inline, sandboxed and at most its grant's bytes.
func TestLinksServeTheFileTheyGrant(t *testing.T) {
	response := get(t, files{opened: application.OpenedWorkspaceFile{Name: "report.pdf", Download: true, Content: io.NopCloser(strings.NewReader("%PDF-1.7"))}})
	require.Equal(t, http.StatusOK, response.StatusCode)
	require.Equal(t, "application/pdf", response.Header.Get("Content-Type"))
	require.Equal(t, `attachment; filename=report.pdf`, response.Header.Get("Content-Disposition"))
	require.Equal(t, "no-store", response.Header.Get("Cache-Control"))
	body, _ := io.ReadAll(response.Body)
	require.Equal(t, "%PDF-1.7", string(body))

	response = get(t, files{opened: application.OpenedWorkspaceFile{Name: "big.log", Limit: 4, Content: io.NopCloser(strings.NewReader("abcdefgh"))}})
	require.Equal(t, `inline; filename=big.log`, response.Header.Get("Content-Disposition"))
	require.Contains(t, response.Header.Get("Content-Security-Policy"), "sandbox")
	body, _ = io.ReadAll(response.Body)
	require.Equal(t, "abcd", string(body))
}

func TestLinksAnswerWhyNothingIsServed(t *testing.T) {
	for err, status := range map[error]int{
		application.ErrInvalidWorkspaceFileLink: http.StatusUnauthorized,
		application.ErrWorkspacePathNotFound:    http.StatusNotFound,
		domain.ErrWorkspaceUnavailable:          http.StatusServiceUnavailable,
		application.ErrWorkspaceClosed:          http.StatusGone,
		io.ErrUnexpectedEOF:                     http.StatusInternalServerError,
	} {
		require.Equal(t, status, get(t, files{err: err}).StatusCode, err.Error())
	}
}
