package infrastructure

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

// Gotenberg's answers map to converter outcomes; its messages go nowhere.
func TestGotenbergAnswers(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   domain.ConverterOutcome
	}{
		{http.StatusTooManyRequests, "", domain.ConverterBusy},
		{http.StatusBadRequest, "The document 'source.doc' is password-protected. Provide its password in the 'password' form field.", domain.ConverterEncrypted},
		{http.StatusBadRequest, "Invalid form data: a password may be required, the exact cause is uncertain", domain.ConverterRefused},
		{http.StatusInternalServerError, "LibreOffice failed to convert the document. The request is valid and may be retried.", domain.ConverterUnavailable},
		{http.StatusServiceUnavailable, "", domain.ConverterTimedOut},
		{http.StatusBadGateway, "", domain.ConverterUnavailable},
	}
	for _, tc := range cases {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			file, header, err := r.FormFile("files")
			require.NoError(t, err)
			file.Close()
			require.Equal(t, "source.doc", header.Filename, "the file name never reaches the converter")
			w.WriteHeader(tc.status)
			_, _ = io.WriteString(w, tc.body)
		}))
		result, pdf, err := NewGotenberg(server.URL, time.Minute, nil).Render(t.Context(), "季度 报告.DOC", strings.NewReader("content"), 1024)
		server.Close()
		require.NoError(t, err)
		require.Nil(t, pdf)
		require.Equal(t, tc.want, result.Outcome, "%d %s", tc.status, tc.body)
	}

	unreachable := NewGotenberg("http://127.0.0.1:1", time.Minute, nil)
	result, _, err := unreachable.Render(t.Context(), "a.docx", strings.NewReader("x"), 1024)
	require.NoError(t, err)
	require.Equal(t, domain.ConverterUnavailable, result.Outcome)
}

// A rendering over the limit comes back with its size and no PDF.
func TestGotenbergRenderingLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "%PDF-1.7 0123456789")
	}))
	defer server.Close()
	converter := NewGotenberg(server.URL, time.Minute, nil)
	result, pdf, err := converter.Render(t.Context(), "a.docx", strings.NewReader("x"), 8)
	require.NoError(t, err)
	require.Nil(t, pdf)
	require.Equal(t, domain.ConverterResult{Outcome: domain.ConverterRendered, PDFBytes: 9}, result)
	result, pdf, err = converter.Render(t.Context(), "a.docx", strings.NewReader("x"), 1024)
	require.NoError(t, err)
	body, _ := io.ReadAll(pdf)
	require.NoError(t, pdf.Close())
	require.Equal(t, "%PDF-1.7 0123456789", string(body))
	require.EqualValues(t, 19, result.PDFBytes)
}
