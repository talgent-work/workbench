// Package http serves the content behind Storage's links: upload links,
// Access Links and Share Links. Each link carries its own authority, so these
// endpoints need no signed-in session.
package http

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/talgent-work/workbench/internal/business/storage/application"
	"github.com/talgent-work/workbench/internal/business/storage/domain"
	"github.com/talgent-work/workbench/pkg/storagepath"
)

// ContentApplication is the Storage use cases these endpoints delegate to.
type ContentApplication interface {
	UploadAttachment(ctx context.Context, token string, content io.Reader) (application.SpaceFile, error)
	ReadAccess(ctx context.Context, token string) (application.OpenedFile, bool, error)
	ReadShared(ctx context.Context, token string) (application.OpenedFile, error)
}

// Endpoints serving each link.
const (
	UploadPath  = "/v1/storage/uploads"
	ContentPath = storagepath.ContentPath
	SharePrefix = "/v1/storage/shares/"
)

// Register mounts the content endpoints on mux; one upload carries at most
// maxUploadBytes.
func Register(mux *http.ServeMux, app ContentApplication, maxUploadBytes int64) {
	mux.HandleFunc("PUT "+UploadPath, func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength > maxUploadBytes {
			writeError(w, &http.MaxBytesError{Limit: maxUploadBytes})
			return
		}
		file, err := app.UploadAttachment(r.Context(), r.URL.Query().Get("grant"), http.MaxBytesReader(w, r.Body, maxUploadBytes))
		if err != nil {
			writeError(w, err)
			return
		}
		setSecurityHeaders(w)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(uploadResponse{Name: file.Position.Name, SizeBytes: file.SizeBytes})
	})
	mux.HandleFunc("GET "+ContentPath, func(w http.ResponseWriter, r *http.Request) {
		opened, download, err := app.ReadAccess(r.Context(), r.URL.Query().Get("grant"))
		if err != nil {
			writeError(w, err)
			return
		}
		serve(w, r, opened, download)
	})
	mux.HandleFunc("GET "+SharePrefix+"{token}", func(w http.ResponseWriter, r *http.Request) {
		opened, err := app.ReadShared(r.Context(), r.PathValue("token"))
		if err != nil {
			writeError(w, err)
			return
		}
		serve(w, r, opened, false)
	})
}

type uploadResponse struct {
	Name      string `json:"name"`
	SizeBytes int64  `json:"sizeBytes"`
}

// serve streams the file under its own name and type, inline or as a
// download, honoring range requests.
func serve(w http.ResponseWriter, r *http.Request, opened application.OpenedFile, download bool) {
	defer opened.Content.Close()
	setSecurityHeaders(w)
	contentType := opened.File.ContentType()
	w.Header().Set("Content-Type", contentType)
	disposition := "inline"
	if download {
		disposition = "attachment"
	}
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": opened.File.Position.Name}))
	if !download {
		// Shown content never runs script in the platform's origin.
		w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; img-src data: blob:; style-src 'unsafe-inline'")
	}
	http.ServeContent(w, r, "", opened.File.ModifiedAt, opened.Content)
}

func setSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
}

func writeError(w http.ResponseWriter, err error) {
	status, code := http.StatusInternalServerError, "storage_unavailable"
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		status, code = http.StatusRequestEntityTooLarge, "file_too_large"
	case errors.Is(err, application.ErrInvalidLink):
		status, code = http.StatusUnauthorized, "invalid_link"
	case errors.Is(err, application.ErrFileNotFound), errors.Is(err, domain.ErrShareLinkNotFound),
		errors.Is(err, application.ErrWorkspaceNotCreated), errors.Is(err, application.ErrWorkspaceReclaimed), errors.Is(err, application.ErrInvalidWorkspacePath):
		status, code = http.StatusNotFound, "not_found"
	case errors.Is(err, domain.ErrShareLinkRevoked):
		status, code = http.StatusGone, "share_link_revoked"
	case errors.Is(err, application.ErrContentChanged):
		status, code = http.StatusConflict, "content_changed"
	case errors.Is(err, application.ErrNameTaken):
		status, code = http.StatusConflict, "name_taken"
	case errors.Is(err, domain.ErrInvalidFileName):
		status, code = http.StatusBadRequest, "invalid_name"
	}
	setSecurityHeaders(w)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]map[string]string{"error": {"code": code, "message": strings.ReplaceAll(code, "_", " ")}})
}
