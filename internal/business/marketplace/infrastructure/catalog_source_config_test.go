package infrastructure_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/marketplace/infrastructure"
)

func acquisition(sources ...infrastructure.CatalogSource) infrastructure.CatalogAcquisitionConfig {
	return infrastructure.CatalogAcquisitionConfig{Workers: 1, RequestTimeoutSeconds: 5, ArchiveBaseURL: "https://catalog.example.invalid/marketplace/archives", Sources: sources}
}

var deployKey = &infrastructure.CatalogSourceCredentials{SSHKeyFile: "/etc/talgent/marketplace-sources/private/ssh-privatekey", KnownHostsFile: "/etc/talgent/marketplace-sources/known_hosts"}

// A private catalog is read with a deploy key from a mounted Secret; its
// packages reach Works only as the platform's own archives, so it must be
// owned, and a source address never carries a credential itself.
func TestCatalogSourceCredentialsRules(t *testing.T) {
	for _, tc := range []struct {
		name   string
		source infrastructure.CatalogSource
		valid  bool
	}{
		{"an owned SSH source with a deploy key", infrastructure.CatalogSource{Name: "private", URI: "git@github.com:example/private-marketplace.git", Owned: true, Credentials: deployKey}, true},
		{"an ssh:// source with a deploy key", infrastructure.CatalogSource{Name: "private", URI: "ssh://git@git.example.invalid/catalog.git#main", Owned: true, Credentials: deployKey}, true},
		{"a public HTTPS source without credentials", infrastructure.CatalogSource{Name: "public", URI: "https://github.com/talgent-work/marketplace.git"}, true},
		{"a credentialed source that is not owned", infrastructure.CatalogSource{Name: "private", URI: "git@github.com:example/private-marketplace.git", Credentials: deployKey}, false},
		{"a deploy key for an HTTPS source", infrastructure.CatalogSource{Name: "private", URI: "https://github.com/example/private-marketplace.git", Owned: true, Credentials: deployKey}, false},
		{"a deploy key without known hosts", infrastructure.CatalogSource{Name: "private", URI: "git@github.com:example/private-marketplace.git", Owned: true, Credentials: &infrastructure.CatalogSourceCredentials{SSHKeyFile: deployKey.SSHKeyFile}}, false},
		{"a relative key file", infrastructure.CatalogSource{Name: "private", URI: "git@github.com:example/private-marketplace.git", Owned: true, Credentials: &infrastructure.CatalogSourceCredentials{SSHKeyFile: "ssh-privatekey", KnownHostsFile: deployKey.KnownHostsFile}}, false},
		{"a token in the address", infrastructure.CatalogSource{Name: "private", URI: "https://x-access-token:secret@github.com/example/private-marketplace.git"}, false},
		{"a token in the query", infrastructure.CatalogSource{Name: "private", URI: "https://catalog.example.invalid/marketplace.json?token=secret"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := acquisition(tc.source).Validate()
			if tc.valid {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.NotContains(t, err.Error(), "secret", "a refusal never echoes the source address")
		})
	}
}
