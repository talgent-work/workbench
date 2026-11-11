package infrastructure

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/talgent-work/workbench/internal/business/marketplace/application"
)

// CatalogSource configures one public catalog source: what publication knows
// of it and where acquisition reads it. Its name is its key in
// CatalogAcquisitionConfig.Sources.
type CatalogSource struct {
	Name     string `json:"-" yaml:"-" toml:"-"`
	URI      string `json:"uri" yaml:"uri" toml:"uri"`
	Priority int    `json:"priority" yaml:"priority" toml:"priority"`
	Owned    bool   `json:"owned" yaml:"owned" toml:"owned"`
}

func (s CatalogSource) publication() application.CatalogSource {
	return application.CatalogSource{Name: s.Name, URI: s.URI, Priority: s.Priority, Owned: s.Owned}
}

// validateSourceAddress keeps every source public: acquisition reads it
// without credentials, so it is an HTTPS address carrying no credential or
// query, a GitHub owner/repository shorthand, or a catalog on disk. SSH and
// other protocols are refused rather than read without a key. A refusal names
// the source, never its address, which may hold a credential.
func validateSourceAddress(source CatalogSource) error {
	address := source.URI
	if !strings.Contains(address, "://") {
		if strings.Contains(address, ":") {
			return fmt.Errorf("marketplace: catalog source %q must be a public HTTPS address; SSH sources are not read", source.Name)
		}
		return nil
	}
	u, e := url.Parse(address)
	if e != nil || u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("marketplace: catalog source %q must be a public HTTPS address", source.Name)
	}
	if u.User != nil || u.RawQuery != "" {
		return fmt.Errorf("marketplace: catalog source %q address cannot carry credentials or a query", source.Name)
	}
	return nil
}
