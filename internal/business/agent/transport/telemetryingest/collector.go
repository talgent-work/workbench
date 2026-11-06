package telemetryingest

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// forwardTimeout bounds one forward to the Collector; the exporter retries a
// request that timed out.
const forwardTimeout = 10 * time.Second

// collectorAnswerLimit bounds the Collector's answer read back.
const collectorAnswerLimit = 64 << 10

// Collector forwards attributed telemetry to the in-cluster OTel Collector
// over OTLP/HTTP protobuf.
type Collector struct {
	http    *http.Client
	address string
}

// NewCollector forwards to the Collector's OTLP HTTP base, such as
// http://otel-collector:4318.
func NewCollector(client *http.Client, address string) (*Collector, error) {
	u, err := url.Parse(address)
	if client == nil || err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("agent: the OTel Collector's OTLP HTTP address is required for Managed telemetry")
	}
	h := *client
	h.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Collector{http: &h, address: strings.TrimRight(u.String(), "/")}, nil
}

// collectorAnswer is what the exporter is answered: the Collector's own
// answer where the exporter can act on it, its partial success, its
// throttling and its refusal of the data, which no retry changes, and a bad
// gateway, which the exporter retries, for anything else.
type collectorAnswer struct {
	status     int
	retryAfter string
	body       []byte
}

func (a collectorAnswer) write(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	if a.status != http.StatusOK {
		if a.retryAfter != "" {
			w.Header().Set("Retry-After", a.retryAfter)
		}
		http.Error(w, http.StatusText(a.status), a.status)
		return
	}
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(a.body)
}

// forward sends one signal's export request to the Collector.
func (c *Collector) forward(ctx context.Context, signal string, body []byte) (collectorAnswer, error) {
	ctx, cancel := context.WithTimeout(ctx, forwardTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.address+signal, bytes.NewReader(body))
	if err != nil {
		return collectorAnswer{}, err
	}
	request.Header.Set("Content-Type", "application/x-protobuf")
	response, err := c.http.Do(request)
	if err != nil {
		return collectorAnswer{}, err
	}
	defer response.Body.Close()
	answer, _ := io.ReadAll(io.LimitReader(response.Body, collectorAnswerLimit))
	_, _ = io.Copy(io.Discard, response.Body)
	switch {
	case response.StatusCode >= 200 && response.StatusCode < 300:
		return collectorAnswer{status: http.StatusOK, body: answer}, nil
	case response.StatusCode == http.StatusTooManyRequests || response.StatusCode == http.StatusServiceUnavailable:
		return collectorAnswer{status: response.StatusCode, retryAfter: response.Header.Get("Retry-After")}, nil
	case response.StatusCode >= 400 && response.StatusCode < 500:
		return collectorAnswer{status: http.StatusBadRequest}, nil
	default:
		return collectorAnswer{status: http.StatusBadGateway}, nil
	}
}
