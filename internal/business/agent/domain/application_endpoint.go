package domain

import (
	"fmt"
	"maps"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/net/http/httpguts"
)

// Application MCP endpoint limits: a Work takes at most this many endpoints,
// each with at most this many headers.
const (
	maxApplicationEndpoints      = 16
	maxApplicationEndpointHeader = 32
	maxEndpointURL               = 2048
	maxEndpointHeaderName        = 128
	maxEndpointHeaderValue       = 8192
)

var endpointName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

// ApplicationMCPEndpoint is an MCP endpoint that an Application gives one of
// its Works at creation, outside the Connector catalog (Application MCP
// endpoint). Only that Work uses it (Endpoint scope). It carries the request
// headers the Application authenticates with, which the Work sends unchanged
// for its whole life; Agent never renews, validates or supplies another
// credential for it (Fixed endpoint headers), the Application keeps them
// valid (Application keeps its endpoints valid), and no read of the Work
// returns them (Endpoint headers stay hidden).
type ApplicationMCPEndpoint struct {
	Name    string
	URL     string
	Headers map[string]string
}

func (e ApplicationMCPEndpoint) Clone() ApplicationMCPEndpoint {
	e.Headers = maps.Clone(e.Headers)
	return e
}

// Validate admits a well-formed endpoint. Its errors name the endpoint and
// never a header value.
func (e ApplicationMCPEndpoint) Validate() error {
	if !endpointName.MatchString(e.Name) {
		return fmt.Errorf("%w: invalid Application MCP endpoint name", ErrInvalidCapabilities)
	}
	address, err := url.Parse(e.URL)
	if err != nil || len(e.URL) > maxEndpointURL || (address.Scheme != "http" && address.Scheme != "https") || address.Host == "" || address.User != nil || expands(e.URL) {
		return fmt.Errorf("%w: Application MCP endpoint %s needs an absolute http or https URL without credentials", ErrInvalidCapabilities, e.Name)
	}
	if len(e.Headers) > maxApplicationEndpointHeader {
		return fmt.Errorf("%w: Application MCP endpoint %s has too many headers", ErrInvalidCapabilities, e.Name)
	}
	for name, value := range e.Headers {
		if len(name) > maxEndpointHeaderName || !httpguts.ValidHeaderFieldName(name) || len(value) > maxEndpointHeaderValue || !httpguts.ValidHeaderFieldValue(value) || expands(value) {
			return fmt.Errorf("%w: Application MCP endpoint %s has an invalid header", ErrInvalidCapabilities, e.Name)
		}
	}
	return nil
}

// expands reports a value the Harness would expand from its environment: the
// Work would no longer send it as the Application gave it, and could send the
// run's environment to the Application.
func expands(value string) bool { return strings.Contains(value, "${") }

// validateApplicationEndpoints admits a Work's endpoints: each well formed,
// within the Work's limit, and named apart from each other and from the
// Work's Connectors, which share the Harness's server names.
func validateApplicationEndpoints(endpoints []ApplicationMCPEndpoint, connectors []SelectedConnector) error {
	if len(endpoints) > maxApplicationEndpoints {
		return fmt.Errorf("%w: too many Application MCP endpoints", ErrInvalidCapabilities)
	}
	names := make(map[string]bool, len(endpoints)+len(connectors))
	for _, connector := range connectors {
		names[strings.ToLower(connector.Name)] = true
	}
	for _, endpoint := range endpoints {
		if err := endpoint.Validate(); err != nil {
			return err
		}
		if names[strings.ToLower(endpoint.Name)] {
			return fmt.Errorf("%w: Application MCP endpoint %s repeats a server name", ErrInvalidCapabilities, endpoint.Name)
		}
		names[strings.ToLower(endpoint.Name)] = true
	}
	return nil
}

func cloneApplicationEndpoints(endpoints []ApplicationMCPEndpoint) []ApplicationMCPEndpoint {
	cloned := slices.Clone(endpoints)
	for i := range cloned {
		cloned[i] = cloned[i].Clone()
	}
	return cloned
}
