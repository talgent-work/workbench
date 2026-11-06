// Package telemetryingest takes the OTLP telemetry a Managed Work's Claude Code
// sends over the internet: it authenticates each request by the run's telemetry
// token, replaces the resource attribution of everything in it with the run's
// own, and forwards it to the OTel Collector, which a Cloud run's sandbox
// reaches directly. It interprets nothing else of the telemetry.
package telemetryingest

import (
	"compress/gzip"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"

	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/pkg/authn"
)

// Prefix is where the controlplane serves the endpoint; the computer route
// maps the machine's /computer/v1/otel to it, and Claude Code appends each
// signal's OTLP path.
const Prefix = "/v1/agent/telemetry"

// Signals the Harness exports; it exports no metrics.
const (
	tracesPath = "/v1/traces"
	logsPath   = "/v1/logs"
)

// Bounds of one request: what is read off the wire, and what a gzip body may
// expand to. Claude Code's batches stay far below them.
const (
	maxWireBytes    = 4 << 20
	maxDecodedBytes = 16 << 20
)

// Register mounts the endpoint on mux.
func Register(mux *http.ServeMux, attributions query.RunTelemetryAttributions, collector *Collector, logger *slog.Logger) {
	h := handler{attributions: attributions, collector: collector, logger: logger}
	mux.HandleFunc("POST "+Prefix+tracesPath, func(w http.ResponseWriter, r *http.Request) {
		h.serve(w, r, tracesPath, attributeTraces)
	})
	mux.HandleFunc("POST "+Prefix+logsPath, func(w http.ResponseWriter, r *http.Request) {
		h.serve(w, r, logsPath, attributeLogs)
	})
}

type handler struct {
	attributions query.RunTelemetryAttributions
	collector    *Collector
	logger       *slog.Logger
}

// attribute decodes one signal's export request, replaces the resource
// attribution of everything in it and encodes it again.
type attribute func(body []byte, attributes map[string]string) ([]byte, error)

var errMalformed = errors.New("telemetry: malformed OTLP request")

func (h handler) serve(w http.ResponseWriter, r *http.Request, signal string, rewrite attribute) {
	// The token is checked before the body is read.
	token, bearer := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !bearer {
		h.reject(w, r, http.StatusUnauthorized, "telemetry token required", nil)
		return
	}
	attributes, err := h.attributions.RunTelemetryAttribution(r.Context(), r.Header.Get(authn.HeaderNativeWorkID), strings.TrimSpace(token))
	switch {
	case errors.Is(err, query.ErrTelemetryTokenRejected):
		h.reject(w, r, http.StatusUnauthorized, "telemetry token rejected", nil)
		return
	case err != nil:
		h.reject(w, r, http.StatusServiceUnavailable, "telemetry unavailable", err)
		return
	}
	if mediaType, _, _ := strings.Cut(r.Header.Get("Content-Type"), ";"); strings.TrimSpace(mediaType) != "application/x-protobuf" {
		h.reject(w, r, http.StatusUnsupportedMediaType, "only OTLP/HTTP protobuf is accepted", nil)
		return
	}
	body, status, err := readBody(w, r)
	if err != nil {
		h.reject(w, r, status, "unreadable telemetry", nil)
		return
	}
	attributed, err := rewrite(body, attributes)
	if err != nil {
		h.reject(w, r, http.StatusBadRequest, "malformed telemetry", nil)
		return
	}
	answer, err := h.collector.forward(r.Context(), signal, attributed)
	if err != nil {
		h.reject(w, r, http.StatusServiceUnavailable, "telemetry unavailable", err)
		return
	}
	answer.write(w)
}

// readBody reads the request within its bounds, gzip decoded.
func readBody(w http.ResponseWriter, r *http.Request) ([]byte, int, error) {
	var reader io.Reader = http.MaxBytesReader(w, r.Body, maxWireBytes)
	switch strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Encoding"))) {
	case "", "identity":
	case "gzip":
		decoded, err := gzip.NewReader(reader)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		defer decoded.Close()
		reader = decoded
	default:
		return nil, http.StatusUnsupportedMediaType, errMalformed
	}
	body, err := io.ReadAll(io.LimitReader(reader, maxDecodedBytes+1))
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge) || len(body) > maxDecodedBytes:
		return nil, http.StatusRequestEntityTooLarge, errMalformed
	case err != nil:
		return nil, http.StatusBadRequest, err
	}
	return body, http.StatusOK, nil
}

// reject answers in plain text, never echoing the request; a failure of the
// platform is logged without the token.
func (h handler) reject(w http.ResponseWriter, r *http.Request, status int, message string, cause error) {
	if cause != nil && !errors.Is(cause, context.Canceled) {
		h.logger.WarnContext(r.Context(), "managed telemetry not forwarded", slog.Int("status", status), slog.Any("error", cause))
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Error(w, message, status)
}

func attributeTraces(body []byte, attributes map[string]string) ([]byte, error) {
	var request coltracepb.ExportTraceServiceRequest
	if err := proto.Unmarshal(body, &request); err != nil {
		return nil, errors.Join(errMalformed, err)
	}
	for _, spans := range request.GetResourceSpans() {
		spans.Resource = attributed(spans.GetResource(), attributes)
	}
	return proto.Marshal(&request)
}

func attributeLogs(body []byte, attributes map[string]string) ([]byte, error) {
	var request collogspb.ExportLogsServiceRequest
	if err := proto.Unmarshal(body, &request); err != nil {
		return nil, errors.Join(errMalformed, err)
	}
	for _, logs := range request.GetResourceLogs() {
		logs.Resource = attributed(logs.GetResource(), attributes)
	}
	return proto.Marshal(&request)
}
