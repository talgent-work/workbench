package infrastructure

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"github.com/samber/oops"

	"github.com/talgent-work/workbench/internal/business/storage/application"
	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

// Gotenberg renders office content to PDF through Gotenberg's LibreOffice
// route (8.37, -libreoffice variant). Its answers map to converter outcomes:
// 429 busy, 400 password-protected or otherwise refused, 503 timed out.
// Gotenberg 8.37 answers 500 alike for a document LibreOffice cannot convert
// and for its own resource faults, calling both retryable, so a 500 records
// nothing: a fault must not block a preview for the retention period.
// Upstream messages never reach a reader; transport failures are logged.
type Gotenberg struct {
	endpoint string
	client   *http.Client
	logger   *slog.Logger
}

var _ application.ConverterPort = (*Gotenberg)(nil)

// NewGotenberg talks to the Gotenberg at address, waiting at most timeout
// for one conversion.
func NewGotenberg(address string, timeout time.Duration, logger *slog.Logger) *Gotenberg {
	if logger == nil {
		logger = slog.Default()
	}
	return &Gotenberg{
		endpoint: strings.TrimRight(address, "/") + "/forms/libreoffice/convert",
		client:   &http.Client{Timeout: timeout},
		logger:   logger.With("component", "storage.gotenberg"),
	}
}

// Render sends the content under a neutral name keeping its extension,
// which tells LibreOffice the format, so no file name reaches the converter.
func (g *Gotenberg) Render(ctx context.Context, name string, content io.Reader, pdfLimit int64) (domain.ConverterResult, io.ReadCloser, error) {
	body, contentType := multipartSource("source"+strings.ToLower(path.Ext(name)), content)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, g.endpoint, body)
	if err != nil {
		return domain.ConverterResult{}, nil, oops.With("operation", "storage.render").Wrap(err)
	}
	request.Header.Set("Content-Type", contentType)
	response, err := g.client.Do(request)
	if err != nil {
		g.logger.WarnContext(ctx, "converter unreachable", "error", err)
		return unreachable(ctx, err), nil, nil
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusOK:
		return g.keepRendering(ctx, response.Body, pdfLimit)
	case http.StatusTooManyRequests:
		return domain.ConverterResult{Outcome: domain.ConverterBusy}, nil, nil
	case http.StatusBadRequest:
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4<<10))
		// Gotenberg 8.37 names a protected document exactly so.
		if bytes.Contains(message, []byte("is password-protected")) {
			return domain.ConverterResult{Outcome: domain.ConverterEncrypted}, nil, nil
		}
		return domain.ConverterResult{Outcome: domain.ConverterRefused}, nil, nil
	case http.StatusServiceUnavailable:
		// Gotenberg answers 503 when its own conversion time runs out.
		return domain.ConverterResult{Outcome: domain.ConverterTimedOut}, nil, nil
	}
	g.logger.WarnContext(ctx, "converter failed", "status", response.StatusCode)
	return domain.ConverterResult{Outcome: domain.ConverterUnavailable}, nil, nil
}

func unreachable(ctx context.Context, err error) domain.ConverterResult {
	var timeout net.Error
	if errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil || (errors.As(err, &timeout) && timeout.Timeout()) {
		return domain.ConverterResult{Outcome: domain.ConverterTimedOut}
	}
	return domain.ConverterResult{Outcome: domain.ConverterUnavailable}
}

// multipartSource streams content as the files form field.
func multipartSource(fileName string, content io.Reader) (io.Reader, string) {
	reader, writer := io.Pipe()
	form := multipart.NewWriter(writer)
	go func() {
		part, err := form.CreateFormFile("files", fileName)
		if err == nil {
			_, err = io.Copy(part, content)
		}
		if err == nil {
			err = form.Close()
		}
		writer.CloseWithError(err)
	}()
	return reader, form.FormDataContentType()
}

// keepRendering spools the PDF to a temporary file, reading at most one byte
// past the limit: an over-limit rendering comes back with its size only.
func (g *Gotenberg) keepRendering(ctx context.Context, body io.Reader, pdfLimit int64) (domain.ConverterResult, io.ReadCloser, error) {
	spool, err := os.CreateTemp("", "talgent-preview-*.pdf")
	if err != nil {
		return domain.ConverterResult{}, nil, oops.With("operation", "storage.render").Wrap(err)
	}
	discard := func() { spool.Close(); _ = os.Remove(spool.Name()) }
	size, err := io.Copy(spool, io.LimitReader(body, pdfLimit+1))
	if err != nil {
		discard()
		g.logger.WarnContext(ctx, "converter answer cut short", "error", err)
		return domain.ConverterResult{Outcome: domain.ConverterUnavailable}, nil, nil
	}
	result := domain.ConverterResult{Outcome: domain.ConverterRendered, PDFBytes: size}
	if size > pdfLimit {
		discard()
		return result, nil, nil
	}
	if _, err := spool.Seek(0, io.SeekStart); err != nil {
		discard()
		return domain.ConverterResult{}, nil, oops.With("operation", "storage.render").Wrap(err)
	}
	return result, &spooledRendering{File: spool}, nil
}

// spooledRendering removes its temporary file when closed.
type spooledRendering struct{ *os.File }

func (s *spooledRendering) Close() error {
	err := s.File.Close()
	if removeErr := os.Remove(s.Name()); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return errors.Join(err, fmt.Errorf("storage: remove spooled rendering: %w", removeErr))
	}
	return err
}
