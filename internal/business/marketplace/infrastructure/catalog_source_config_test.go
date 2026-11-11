package infrastructure_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/marketplace/infrastructure"
)

func acquisition(sources ...infrastructure.CatalogSource) infrastructure.CatalogAcquisitionConfig {
	named := make(map[string]infrastructure.CatalogSource, len(sources))
	for _, source := range sources {
		named[source.Name] = source
	}
	return infrastructure.CatalogAcquisitionConfig{Workers: 1, RequestTimeoutSeconds: 5, ArchiveBaseURL: "https://catalog.example.invalid/marketplace/archives", Sources: named}
}

// Every catalog source is public: it is read over HTTPS without credentials,
// and its address never carries a credential itself.
func TestCatalogSourceAddressRules(t *testing.T) {
	for _, tc := range []struct {
		name  string
		uri   string
		valid bool
	}{
		{"a public HTTPS repository", "https://github.com/talgent-work/marketplace.git", true},
		{"a public HTTPS repository at a ref", "https://git.example.invalid/catalog.git#main", true},
		{"a public HTTPS catalog file", "https://catalog.example.invalid/marketplace.json", true},
		{"a GitHub repository shorthand", "talgent-work/marketplace", true},
		{"a catalog directory on disk", "/srv/catalogs/marketplace", true},
		{"a token in the address", "https://x-access-token:secret@github.com/example/marketplace.git", false},
		{"a token in the query", "https://catalog.example.invalid/marketplace.json?token=secret", false},
		{"an scp-like SSH address", "git@github.com:example/secret-marketplace.git", false},
		{"an ssh:// address", "ssh://git@git.example.invalid/secret-catalog.git#main", false},
		{"a plain HTTP address", "http://catalog.example.invalid/secret-marketplace.git", false},
		{"a git protocol address", "git://git.example.invalid/secret-catalog.git", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, owned := range []bool{false, true} {
				err := acquisition(infrastructure.CatalogSource{Name: "source", URI: tc.uri, Owned: owned}).Validate()
				if tc.valid {
					require.NoError(t, err)
					continue
				}
				require.Error(t, err)
				require.Contains(t, err.Error(), `"source"`, "a refusal names the source")
				require.NotContains(t, err.Error(), "secret", "a refusal never echoes the source address")
			}
		})
	}
}
