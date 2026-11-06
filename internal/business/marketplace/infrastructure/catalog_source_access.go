package infrastructure

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/talgent-work/workbench/internal/business/marketplace/application"
)

// CatalogSource configures one catalog source: what publication knows of it
// and, for a private catalog, how acquisition reads it.
type CatalogSource struct {
	Name     string `json:"name" yaml:"name" toml:"name"`
	URI      string `json:"uri" yaml:"uri" toml:"uri"`
	Priority int    `json:"priority" yaml:"priority" toml:"priority"`
	Owned    bool   `json:"owned" yaml:"owned" toml:"owned"`
	// Credentials read a private catalog; absent for a public one.
	Credentials *CatalogSourceCredentials `json:"credentials" yaml:"credentials" toml:"credentials"`
}

// CatalogSourceCredentials name the mounted Secret files that read a private
// catalog over SSH: a read-only deploy key and the hosts it may be offered to.
// Only the catalog's own repository is read with them; the packages it lists
// reach Works as the platform's archives, never with the key.
type CatalogSourceCredentials struct {
	SSHKeyFile     string `json:"ssh-key-file" yaml:"ssh-key-file" toml:"ssh-key-file"`
	KnownHostsFile string `json:"known-hosts-file" yaml:"known-hosts-file" toml:"known-hosts-file"`
}

func (s CatalogSource) publication() application.CatalogSource {
	return application.CatalogSource{Name: s.Name, URI: s.URI, Priority: s.Priority, Owned: s.Owned}
}

// scpRemote is git's scp-like SSH address, user@host:path.
var scpRemote = regexp.MustCompile(`^[A-Za-z0-9._-]+@[A-Za-z0-9.-]+:[^:].*$`)

func sshRemote(uri string) bool {
	if strings.HasPrefix(uri, "ssh://") {
		return true
	}
	return !strings.Contains(uri, "://") && scpRemote.MatchString(uri)
}

// validateSourceAccess keeps credentials out of source addresses: a private
// catalog names its Secret files instead, is read over SSH, and is owned, so
// its packages reach Works as the platform's archives and never need the key.
func validateSourceAccess(source CatalogSource) error {
	if u, e := url.Parse(source.URI); e == nil && (u.Scheme == "https" || u.Scheme == "http") && (u.User != nil || u.RawQuery != "") {
		return errors.New("marketplace: a catalog source address cannot carry credentials or a query")
	}
	credentials := source.Credentials
	if credentials == nil {
		return nil
	}
	if !source.Owned {
		return errors.New("marketplace: a credentialed catalog source must be owned")
	}
	if !sshRemote(source.URI) {
		return errors.New("marketplace: a credentialed catalog source is read over SSH")
	}
	if !filepath.IsAbs(credentials.SSHKeyFile) || !filepath.IsAbs(credentials.KnownHostsFile) {
		return errors.New("marketplace: a credentialed catalog source needs absolute deploy key and known hosts files")
	}
	return nil
}

// forSource is the acquisition that reads one source's catalog. A private
// source's git commands offer its deploy key to its known hosts alone, from
// private copies whose mode SSH accepts, and its results are kept apart from
// unauthenticated reads: a package entry naming the same repository is still
// read without the key, so it cannot reach a Work that would need one.
func (a *catalogAcquisition) forSource(source CatalogSource) (*catalogAcquisition, error) {
	credentials := source.Credentials
	if credentials == nil {
		return a, nil
	}
	directory := filepath.Join(a.root, "access-"+source.Name)
	if e := os.Mkdir(directory, 0o700); e != nil {
		return nil, e
	}
	key, known := filepath.Join(directory, "key"), filepath.Join(directory, "known_hosts")
	for target, origin := range map[string]string{key: credentials.SSHKeyFile, known: credentials.KnownHostsFile} {
		body, e := os.ReadFile(origin)
		if e != nil {
			return nil, errors.New("marketplace: catalog source credentials are unreadable")
		}
		if e := os.WriteFile(target, body, 0o600); e != nil {
			return nil, e
		}
	}
	command := a.command
	command.environment = append(append([]string(nil), a.command.environment...), "GIT_SSH_COMMAND=ssh -F /dev/null -i "+shellQuote(key)+
		" -o IdentitiesOnly=yes -o IdentityAgent=none -o BatchMode=yes -o StrictHostKeyChecking=yes -o UserKnownHostsFile="+shellQuote(known)+
		" -o GlobalKnownHostsFile=/dev/null")
	view := *a
	view.command, view.scope = command, "source:"+source.Name+":"
	return &view, nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
