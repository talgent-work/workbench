package infrastructure

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/talgent-work/workbench/internal/pkg/pluginconfig"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gofrs/flock"
	"github.com/talgent-work/workbench/internal/business/marketplace/application"
)

type CatalogAcquisitionConfig struct {
	// Sources are keyed by name, so a configuration profile adds sources
	// beside the ones it inherits instead of listing them again.
	Sources               map[string]CatalogSource `json:"sources" yaml:"sources" toml:"sources"`
	RequestTimeoutSeconds int                      `json:"request-timeout-seconds" yaml:"request-timeout-seconds" toml:"request-timeout-seconds"`
	Workers               int                      `json:"workers" yaml:"workers" toml:"workers"`
	ArchiveBaseURL        string                   `json:"archive-base-url" yaml:"archive-base-url" toml:"archive-base-url"`
	CertificateAuthority  string                   `json:"certificate-authority" yaml:"certificate-authority" toml:"certificate-authority"`
}

var catalogName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,127}$`)

func (c CatalogAcquisitionConfig) Validate() error {
	if c.RequestTimeoutSeconds <= 0 || c.Workers < 1 || c.Workers > 8 || len(c.Sources) == 0 {
		return errors.New("marketplace: sources, positive request timeout and 1–8 workers are required")
	}
	for _, source := range c.sources() {
		if !catalogName.MatchString(source.Name) || strings.TrimSpace(source.URI) == "" || strings.HasPrefix(source.URI, "-") {
			return fmt.Errorf("marketplace: invalid catalog source %q", source.Name)
		}
		if e := validateSourceAddress(source); e != nil {
			return e
		}
		if source.Owned {
			base, e := url.Parse(c.ArchiveBaseURL)
			if e != nil || base.Scheme != "https" || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
				return errors.New("marketplace: owned archive base URL must use HTTPS without credentials, query or fragment")
			}
		}
	}
	return nil
}

// sources are the configured sources, each named by its key, in name order.
func (c CatalogAcquisitionConfig) sources() []CatalogSource {
	sources := make([]CatalogSource, 0, len(c.Sources))
	for name, source := range c.Sources {
		source.Name = name
		sources = append(sources, source)
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].Name < sources[j].Name })
	return sources
}

type CatalogSynchronizer struct {
	config CatalogAcquisitionConfig
	force  bool
	store  *CatalogStore
}

func NewCatalogSynchronizer(config CatalogAcquisitionConfig, store *CatalogStore) (*CatalogSynchronizer, error) {
	if e := config.Validate(); e != nil {
		return nil, e
	}
	return &CatalogSynchronizer{config: config, store: store}, nil
}

var _ application.PluginSourcePort = (*CatalogSynchronizer)(nil)

// SynchronizeCatalog copies configuration so Force never mutates a shared instance.
func (s *CatalogSynchronizer) SynchronizeCatalog(ctx context.Context, force bool) error {
	local := *s
	local.force = force
	return local.synchronize(ctx)
}

func (s *CatalogSynchronizer) synchronize(ctx context.Context) error {
	lock := flock.New(filepath.Join(s.store.directory, "synchronization.lock"))
	locked, e := lock.TryLockContext(ctx, 50*time.Millisecond)
	if e != nil {
		return e
	}
	if !locked {
		return ctx.Err()
	}
	defer lock.Unlock()
	a, e := newCatalogAcquisition(s.config)
	if e != nil {
		return e
	}
	defer a.close()
	// Leave a bounded publication window so an acquisition deadline retains failed
	// Plugins while publishing completed ones. Explicit cancellation still stops all work.
	acquireCtx := ctx
	cancel := func() {}
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		reserve := min(10*time.Second, remaining/10)
		acquireCtx, cancel = context.WithDeadline(ctx, deadline.Add(-reserve))
	}
	defer cancel()
	sources := make([]SourceReleases, 0, len(s.config.Sources))
	for _, source := range s.config.sources() {
		result, e := s.readSource(acquireCtx, a, source)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		result.Source = source.publication()
		result.Failure = e
		sources = append(sources, result)
	}
	return s.store.Publish(ctx, sources)
}

type catalogIndex struct {
	Name     string `json:"name"`
	Metadata struct {
		PluginRoot string `json:"pluginRoot"`
	} `json:"metadata"`
	Plugins []json.RawMessage `json:"plugins"`
}
type catalogLocation struct {
	directory, remote, commit string
	manifest                  []byte
}

func (s *CatalogSynchronizer) readSource(ctx context.Context, a *catalogAcquisition, source CatalogSource) (SourceReleases, error) {
	location, e := a.catalog(ctx, source.URI)
	if e != nil {
		return SourceReleases{}, e
	}
	var index catalogIndex
	if len(location.manifest) > 16<<20 || json.Unmarshal(location.manifest, &index) != nil || !catalogName.MatchString(index.Name) || len(index.Plugins) > 2000 {
		return SourceReleases{}, errors.New("marketplace: invalid catalog")
	}
	previous, e := s.store.CurrentSource(ctx, source.Name)
	if e != nil {
		return SourceReleases{}, e
	}
	retained := map[string]application.PluginRelease{}
	for _, r := range previous {
		retained[r.Slug] = r
		if r.CatalogEntryName != "" {
			retained[r.CatalogEntryName] = r
		}
	}
	result := SourceReleases{Source: source.publication(), Failures: map[string]error{}, Skipped: map[string]error{}}
	type job struct {
		order int
		slug  string
		entry json.RawMessage
	}
	jobs := make(chan job, len(index.Plugins))
	seen := map[string]bool{}
	for order, entry := range index.Plugins {
		var meta struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(entry, &meta) != nil || !pluginconfig.ValidPluginName(meta.Name) {
			result.Failures["invalid-entry"] = errors.New("invalid plugin slug")
			continue
		}
		if seen[meta.Name] {
			result.Failures[meta.Name] = errors.New("duplicate plugin slug")
			continue
		}
		seen[meta.Name] = true
		jobs <- job{order, meta.Name, entry}
	}
	close(jobs)
	type outcome struct {
		order   int
		release application.PluginRelease
		slug    string
		err     error
	}
	outcomes := []outcome{}
	var mu sync.Mutex
	var workers sync.WaitGroup
	for range s.config.Workers {
		workers.Go(func() {
			for j := range jobs {
				release, slug, e := s.readPlugin(ctx, a, source, location, index.Metadata.PluginRoot, j.entry, retained)
				mu.Lock()
				outcomes = append(outcomes, outcome{j.order, release, slug, e})
				mu.Unlock()
			}
		})
	}
	workers.Wait()
	// Prefer an entry already using the package name; otherwise preserve upstream
	// order. Presentation differences between aliases cannot choose a race winner.
	sort.SliceStable(outcomes, func(i, j int) bool {
		canonicalI, canonicalJ := outcomes[i].release.CatalogEntryName == "", outcomes[j].release.CatalogEntryName == ""
		if canonicalI != canonicalJ {
			return canonicalI
		}
		return outcomes[i].order < outcomes[j].order
	})
	chosen := map[string]application.PluginRelease{}
	for _, item := range outcomes {
		if errors.Is(item.err, errLocalOnlySource) {
			result.Skipped[item.slug] = item.err
			continue
		}
		if item.err != nil {
			result.Failures[item.slug] = item.err
			continue
		}
		if existing, ok := chosen[item.slug]; ok {
			if !sameAcquiredPackage(existing, item.release) {
				result.Failures[item.slug] = errors.New("marketplace: conflicting entries for the same package name")
			}
			continue
		}
		chosen[item.slug] = item.release
		result.Releases = append(result.Releases, item.release)
	}

	return result, nil
}
func (s *CatalogSynchronizer) readPlugin(ctx context.Context, a *catalogAcquisition, source CatalogSource, location catalogLocation, pluginRoot string, entry json.RawMessage, retained map[string]application.PluginRelease) (release application.PluginRelease, slug string, err error) {
	var meta packageIdentity
	if e := json.Unmarshal(entry, &meta); e != nil {
		return release, "invalid-entry", e
	}
	slug = meta.Name
	previous := retained[slug]
	if previous.Slug != "" {
		slug = previous.Slug
	}
	if e := ctx.Err(); e != nil {
		return release, slug, e
	}
	if localOnlySourceKinds[entrySourceKind(entry)] {
		return release, slug, errLocalOnlySource
	}
	// A verified checksum proves the package itself is unchanged, independently
	// of an unreliable index version. Otherwise inspect its manifest first.
	if !s.force && previous.Slug != "" && meta.Version == previous.Version && samePinnedArchive(entry, previous.NativeEntry) {
		return previous, previous.Slug, nil
	}
	// An entry naming no version may describe a package naming none, whose
	// release is its pin. Pinning a third-party entry first reads nothing the
	// package read below would not, and an unchanged pin needs no package read.
	var pinned json.RawMessage
	var e error
	if meta.Version == "" && !source.Owned {
		if pinned, e = s.pinEntry(ctx, a, source, location, pluginRoot, entry); e != nil {
			return release, slug, e
		}
		if !s.force && previous.Slug != "" && unchangedPin(pinned, previous.NativeEntry, previous.Version) {
			return previous, previous.Slug, nil
		}
	}
	identity, e := a.manifestIdentity(ctx, location, pluginRoot, entry)
	if e != nil {
		return release, slug, e
	}
	if identity.Name == "" {
		identity.Name = meta.Name
	}
	if identity.Version == "" {
		identity.Version = meta.Version
	}
	if !pluginconfig.ValidPluginName(identity.Name) {
		return release, slug, errors.New("marketplace: invalid package name")
	}
	slug = identity.Name
	previous = retained[slug]
	if identity.Version == "" {
		// Owned packages pin only after this point: their pin builds an archive.
		if pinned == nil {
			if pinned, e = s.pinEntry(ctx, a, source, location, pluginRoot, entry); e != nil {
				return release, slug, e
			}
		}
		if identity.Version, e = pinnedVersion(pinned); e != nil {
			return release, slug, e
		}
		if !s.force && previous.Slug != "" && unchangedPin(pinned, previous.NativeEntry, previous.Version) {
			return previous, slug, nil
		}
		if pinned, e = withVersion(pinned, identity.Version); e != nil {
			return release, slug, e
		}
	} else if !s.force && previous.Slug != "" && identity.Version == previous.Version {
		return previous, slug, nil
	}
	if pinned == nil {
		if pinned, e = s.pinEntry(ctx, a, source, location, pluginRoot, entry); e != nil {
			return release, slug, e
		}
	}
	directory, e := a.plugin(ctx, pinned)
	if e != nil {
		return release, slug, e
	}
	var value struct {
		Source json.RawMessage `json:"source"`
	}
	if e := json.Unmarshal(pinned, &value); e != nil {
		return release, slug, e
	}
	release, e = ParsePluginMetadata(directory, source.Name, string(value.Source), pinned)
	if e == nil && meta.Name != release.Slug {
		release.CatalogEntryName = meta.Name
	}
	return release, slug, e
}

func readCatalogManifest(directory string) ([]byte, error) {
	return os.ReadFile(filepath.Join(directory, ".claude-plugin/marketplace.json"))
}

// A verified archive checksum makes another manifest fetch unnecessary. Mutable
// source URLs and index versions alone cannot establish an unchanged package.
func samePinnedArchive(left, right []byte) bool {
	type entry struct {
		Source struct {
			Kind   string `json:"source"`
			Digest string `json:"sha256"`
		} `json:"source"`
	}
	var a, b entry
	return json.Unmarshal(left, &a) == nil && json.Unmarshal(right, &b) == nil &&
		a.Source.Kind == "archive" && b.Source.Kind == "archive" &&
		archiveDigest.MatchString(a.Source.Digest) && strings.EqualFold(a.Source.Digest, b.Source.Digest)
}

// Catalog aliases may describe the same fixed package differently. Only display
// metadata can differ; native behavior, components and parameters must agree.
func sameAcquiredPackage(left, right application.PluginRelease) bool {
	a, b := application.AssemblePluginReleaseDTO(left), application.AssemblePluginReleaseDTO(right)
	a.NativeEntry, b.NativeEntry = nil, nil
	a.Description, b.Description = "", ""
	if !reflect.DeepEqual(a, b) {
		return false
	}
	var x, y map[string]json.RawMessage
	if json.Unmarshal(left.NativeEntry, &x) != nil || json.Unmarshal(right.NativeEntry, &y) != nil {
		return false
	}
	for _, key := range []string{"name", "version", "description", "displayName", "category", "author", "homepage", "keywords", "tags"} {
		delete(x, key)
		delete(y, key)
	}
	return reflect.DeepEqual(x, y)
}
