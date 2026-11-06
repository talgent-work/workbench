// Package execd implements OpenSandbox execd's authenticated WebSocket pipe API.
// It transports process bytes and facts; callers own protocol and lifecycle policy.
package execd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/samber/oops"
	"golang.org/x/net/websocket"
)

const accessHeader = "X-EXECD-ACCESS-TOKEN"

type Client struct {
	http  *http.Client
	base  *url.URL
	token string
}

// NewClient takes the endpoint and access token resolved for one sandbox.
// Redirects are rejected so sandbox credentials cannot follow another endpoint.
func NewClient(client *http.Client, endpoint, token string) (*Client, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.TrimSpace(token) == "" || client == nil {
		return nil, errors.New("execd: HTTP client, HTTP(S) endpoint and access token required")
	}
	h := *client
	h.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{http: &h, base: u, token: token}, nil
}

// Create reserves a process handle. The command starts on Attach, allowing the
// caller to persist its handle before launching the native process.
func (c *Client) Create(ctx context.Context, cwd, command string) (string, error) {
	if strings.TrimSpace(cwd) == "" || strings.TrimSpace(command) == "" {
		return "", errors.New("execd: working directory and command required")
	}
	body, err := c.request(ctx, http.MethodPost, c.endpoint("pty"), struct {
		Cwd     string `json:"cwd"`
		Command string `json:"command"`
	}{cwd, command})
	if err != nil {
		return "", err
	}
	var result struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", oops.With("operation", "create").Wrap(err)
	}
	if !validID(result.SessionID) {
		return "", errors.New("execd: invalid process handle")
	}
	return result.SessionID, nil
}

// Attach opens an exclusive pipe connection. It never takes over another owner.
// Closing the connection detaches; Delete is an explicit, separate operation.
func (c *Client) Attach(ctx context.Context, id string, offset int64) (*Pipe, error) {
	if offset < 0 || !validID(id) {
		return nil, errors.New("execd: invalid process handle")
	}
	u := c.endpoint("pty", id, "ws")
	u.RawQuery = fmt.Sprintf("pty=0&since=%d", offset)
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else {
		u.Scheme = "ws"
	}
	config, err := websocket.NewConfig(u.String(), c.base.String())
	if err != nil {
		return nil, oops.With("operation", "attach").Wrap(err)
	}
	config.Header.Set(accessHeader, c.token)
	// Reuse the process HTTP transport's TLS policy for private sandbox endpoints.
	if transport, ok := c.http.Transport.(*http.Transport); ok {
		config.TlsConfig = transport.TLSClientConfig
	}
	conn, err := config.DialContext(ctx)
	if err != nil {
		var dial *websocket.DialError
		if errors.As(err, &dial) {
			err = dialError{dial}
		}
		return nil, oops.With("operation", "attach").Wrap(err)
	}
	return &Pipe{conn: conn, id: id}, nil
}

// dialError exposes a websocket dial's cause, such as cancellation, which
// websocket.DialError does not unwrap.
type dialError struct{ *websocket.DialError }

func (e dialError) Unwrap() error { return e.Err }

// Delete is idempotent for a fixed process handle: a lost deletion response
// must not strand cleanup when recovery finds that handle already absent.
func (c *Client) Delete(ctx context.Context, id string) error {
	if !validID(id) {
		return errors.New("execd: invalid process handle")
	}
	_, err := c.request(ctx, http.MethodDelete, c.endpoint("pty", id), nil)
	var response *responseError
	if errors.As(err, &response) && response.status == http.StatusNotFound {
		return nil
	}
	return err
}

type responseError struct {
	method string
	status int
}

func (e *responseError) Error() string {
	return fmt.Sprintf("execd: %s returned HTTP %d", e.method, e.status)
}

func (c *Client) endpoint(parts ...string) *url.URL { return c.base.JoinPath(parts...) }

func validID(id string) bool {
	if id == "" {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

func (c *Client) request(ctx context.Context, method string, endpoint *url.URL, value any) ([]byte, error) {
	var body []byte
	if value != nil {
		var err error
		body, err = json.Marshal(value)
		if err != nil {
			return nil, oops.With("operation", method).Wrap(err)
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return nil, oops.With("operation", method).Wrap(err)
	}
	req.Header.Set(accessHeader, c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, oops.With("operation", method).Wrap(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Provider bodies may contain command output or credentials.
		return nil, &responseError{method: method, status: resp.StatusCode}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024+1))
	if err != nil {
		return nil, oops.With("operation", method).Wrap(err)
	}
	if len(data) > 64*1024 {
		return nil, errors.New("execd: response too large")
	}
	return data, nil
}
