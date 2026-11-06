// Package workspacefile serves the links to a Managed Work's files: each link
// carries its own authority, so the endpoint needs no signed-in session, as
// Storage's content endpoint needs none.
package workspacefile

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"path"

	"github.com/talgent-work/workbench/internal/business/agent/application"
)

// Path is where the controlplane serves the links; the public route maps the
// reader's /api/v1/agent/workspace-files to it.
const Path = "/v1/agent/workspace-files"

// Files opens the file a link grants.
type Files interface {
	Open(ctx context.Context, token string) (application.OpenedWorkspaceFile, error)
}

// Register mounts the endpoint on mux.
func Register(mux *http.ServeMux, files Files, logger *slog.Logger) {
	mux.HandleFunc("GET "+Path, func(w http.ResponseWriter, r *http.Request) {
		opened, err := files.Open(r.Context(), r.URL.Query().Get("grant"))
		if err != nil {
			writeError(w, r, err, logger)
			return
		}
		defer opened.Content.Close()
		serve(w, opened)
	})
}

// serve streams the file under its own name and the type its name gives,
// inline or as a download, at most its grant's bytes.
func serve(w http.ResponseWriter, opened application.OpenedWorkspaceFile) {
	securityHeaders(w)
	contentType := mime.TypeByExtension(path.Ext(opened.Name))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	disposition := "inline"
	if opened.Download {
		disposition = "attachment"
	} else {
		// Shown content never runs script in the platform's origin.
		w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; img-src data: blob:; style-src 'unsafe-inline'")
	}
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": opened.Name}))
	var content io.Reader = opened.Content
	if opened.Limit > 0 {
		content = io.LimitReader(content, opened.Limit)
	}
	w.WriteHeader(http.StatusOK)
	// A machine lost mid-stream ends the response early; the reader retries.
	_, _ = io.Copy(w, content)
}

func securityHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
}

func writeError(w http.ResponseWriter, r *http.Request, err error, logger *slog.Logger) {
	status, message := http.StatusInternalServerError, "the file is unavailable"
	switch {
	case errors.Is(err, application.ErrInvalidWorkspaceFileLink):
		status, message = http.StatusUnauthorized, "invalid link"
	case errors.Is(err, application.ErrWorkspacePathNotFound), errors.Is(err, application.ErrInvalidWorkspacePath):
		status, message = http.StatusNotFound, "not found"
	case errors.Is(err, application.ErrWorkspaceClosed):
		status, message = http.StatusGone, "the Work is closed"
	case errors.Is(err, application.ErrWorkspaceUnavailable):
		status, message = http.StatusServiceUnavailable, "the Computer is not reachable"
	case errors.Is(err, context.Canceled):
		return
	default:
		// The link is a grant; only the failure is logged.
		logger.WarnContext(r.Context(), "workspace file not served", slog.Any("error", err))
	}
	securityHeaders(w)
	http.Error(w, message, status)
}
