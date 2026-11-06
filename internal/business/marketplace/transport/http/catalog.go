package httpapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/talgent-work/workbench/internal/business/marketplace/application"
)

const ContentPath = "/marketplace/"

// Catalog content is platform-published and public. Personal records are
// available only through the authenticated Marketplace Connect service.
type CatalogHandler struct {
	app    *application.Application
	logger *slog.Logger
}

func NewCatalogHandler(app *application.Application, logger *slog.Logger) *CatalogHandler {
	return &CatalogHandler{app: app, logger: logger}
}

func (h *CatalogHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Path == ContentPath+"marketplace.json" {
		data, err := h.app.Marketplace(r.Context())
		if err != nil {
			h.writeError(w, r, err)
			return
		}
		digest := sha256.Sum256(data)
		w.Header().Set("ETag", `"`+hex.EncodeToString(digest[:])+`"`)
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Content-Type", "application/json")
		http.ServeContent(w, r, "marketplace.json", time.Time{}, bytes.NewReader(data))
		return
	}
	name, ok := strings.CutPrefix(r.URL.Path, ContentPath+"archives/")
	if !ok || !strings.HasSuffix(name, ".zip") {
		http.NotFound(w, r)
		return
	}
	digest := strings.TrimSuffix(name, ".zip")
	content, err := h.app.OpenArchive(r.Context(), digest)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	defer content.Close()
	w.Header().Set("ETag", `"`+digest+`"`)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, name, time.Time{}, content)
}

func (h *CatalogHandler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, application.ErrArchiveNotFound), errors.Is(err, application.ErrInvalidQuery):
		http.NotFound(w, r)
	case errors.Is(err, application.ErrCatalogNotReady):
		http.Error(w, "catalog is not ready", http.StatusServiceUnavailable)
	default:
		h.logger.ErrorContext(r.Context(), "marketplace content request failed", slog.Any("error", err))
		http.Error(w, "catalog content unavailable", http.StatusInternalServerError)
	}
}
