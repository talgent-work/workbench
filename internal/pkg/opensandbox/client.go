// Package opensandbox owns the shared lifecycle API client. Execd process I/O
// uses the separate execd client and never passes through the lifecycle server.
package opensandbox

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	sdk "github.com/alibaba/OpenSandbox/sdks/sandbox/go"
)

type Config struct {
	Address string `json:"address" yaml:"address" toml:"address"`
	APIKey  string `json:"api-key" yaml:"api-key" toml:"api-key"`
}

func (c Config) Validate() error {
	u, err := url.Parse(c.Address)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.TrimSpace(c.APIKey) == "" {
		return errors.New("opensandbox: HTTP(S) lifecycle address and API key are required")
	}
	return nil
}

func NewLifecycleClient(client *http.Client, config Config) (*sdk.LifecycleClient, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if client == nil {
		return nil, errors.New("opensandbox: HTTP client is required")
	}
	h := *client
	h.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	// Disable endpoint caching: resolution belongs to this exact sandbox and
	// the caller's deadline. The SDK cache detaches fetches from that deadline.
	// Do not enable automatic retries of non-idempotent create requests.
	return sdk.NewLifecycleClientWithCache(strings.TrimRight(config.Address, "/"), config.APIKey, nil, sdk.WithHTTPClient(&h)), nil
}
