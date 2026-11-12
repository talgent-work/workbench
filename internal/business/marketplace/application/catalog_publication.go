package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"

	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

type CatalogSource struct {
	Name     string `json:"name" yaml:"name" toml:"name"`
	URI      string `json:"uri" yaml:"uri" toml:"uri"`
	Priority int    `json:"priority" yaml:"priority" toml:"priority"`
	Owned    bool   `json:"owned" yaml:"owned" toml:"owned"`
}

type SourceReleases struct {
	Source   CatalogSource
	Releases []PluginRelease
	// Failed slugs retain their last valid publication from this source.
	Failures map[string]error
	// Skipped slugs name packages the platform can never acquire, such as a
	// command run on the installing machine. They are neither published nor
	// failures; publication only logs them.
	Skipped map[string]error
	// Failure means source enumeration failed; preserve the entire source.
	Failure error
}

type CatalogPublicationPort interface {
	CurrentSource(context.Context, string) ([]PluginRelease, error)
	PublishedRelease(context.Context, string, string, string) (*PluginRelease, error)
	RetainRelease(context.Context, PluginRelease) error
	CommitCatalog(context.Context, []SourceReleases) error
	// RetireSources withdraws everything of each source not named: its
	// releases, its catalog entries, and each archive no remaining release
	// references. Retiring an already retired source changes nothing.
	RetireSources(ctx context.Context, configured []string) (SourceRetirement, error)
}

// SourceRetirement is what one publication withdrew of removed sources.
type SourceRetirement struct {
	Releases []RetiredRelease
	// Archives are the digests of the archives removed from the content store.
	Archives []string
}

type RetiredRelease struct {
	Source, Slug, Version string
}

type PluginSyncFailure struct {
	Source, Slug string
	Cause        error
}

func (e *PluginSyncFailure) Error() string {
	return fmt.Sprintf("marketplace: synchronization failed for %s/%s", e.Source, e.Slug)
}
func (e *PluginSyncFailure) Unwrap() error { return e.Cause }

// PublishCatalog applies release decisions independently, then makes the complete
// selected view visible in one commit. The sources given are the configured
// ones: once the view no longer lists any other source, everything of those
// is retired. A partial failure is reported after commit.
func PublishCatalog(ctx context.Context, store CatalogPublicationPort, sources []SourceReleases, logger *slog.Logger) error {
	var failures []error
	output := make([]SourceReleases, 0, len(sources))
	policy := domain.PluginReleasePolicy{}
	for _, source := range sources {
		previous, err := store.CurrentSource(ctx, source.Source.Name)
		if err != nil {
			return err
		}
		retained := map[string]PluginRelease{}
		for _, release := range previous {
			retained[release.Slug] = release
		}
		next := SourceReleases{Source: source.Source}
		for slug, reason := range source.Skipped {
			logger.WarnContext(ctx, "catalog skipped a plugin the platform cannot acquire", "source", source.Source.Name, "slug", slug, "reason", SafeDiagnosis(reason.Error()))
		}
		if source.Failure != nil {
			next.Releases = previous
			failures = append(failures, &PluginSyncFailure{Source: source.Source.Name, Cause: source.Failure})
		} else {
			choices := map[string]PluginRelease{}
			for slug, failure := range source.Failures {
				var old *domain.PluginRelease
				if value, ok := retained[slug]; ok {
					v := AssemblePluginReleaseDTO(value)
					old = &v
				}
				available, err := policy.DetermineUpdateResult(nil, old, failure)
				if available != nil {
					choices[slug] = retained[slug]
				}
				failures = append(failures, &PluginSyncFailure{Source: source.Source.Name, Slug: slug, Cause: err})
			}
			for _, release := range source.Releases {
				if _, failed := source.Failures[release.Slug]; failed {
					continue
				}
				published, err := store.PublishedRelease(ctx, source.Source.Name, release.Slug, release.Version)
				if err != nil {
					return err
				}
				var prior *domain.PluginRelease
				if published != nil {
					v := AssemblePluginReleaseDTO(*published)
					prior = &v
				}
				candidate := AssemblePluginReleaseDTO(release)
				acceptance := policy.AcceptRelease(candidate, prior)
				if release.Source != source.Source.Name {
					acceptance = domain.ErrInvalidRelease
				}
				var old *domain.PluginRelease
				if value, ok := retained[release.Slug]; ok {
					v := AssemblePluginReleaseDTO(value)
					old = &v
				}
				available, failure := policy.DetermineUpdateResult(&candidate, old, acceptance)
				if failure != nil {
					failures = append(failures, &PluginSyncFailure{Source: source.Source.Name, Slug: release.Slug, Cause: failure})
					if available != nil {
						choices[release.Slug] = retained[release.Slug]
					}
					continue
				}
				if err := store.RetainRelease(ctx, release); err != nil {
					return err
				}
				choices[release.Slug] = release
			}
			for _, release := range choices {
				next.Releases = append(next.Releases, release)
			}
			sort.Slice(next.Releases, func(i, j int) bool { return next.Releases[i].Slug < next.Releases[j].Slug })
		}
		output = append(output, next)
	}
	if err := store.CommitCatalog(ctx, output); err != nil {
		return err
	}
	configured := make([]string, 0, len(sources))
	for _, source := range sources {
		configured = append(configured, source.Source.Name)
	}
	retired, err := store.RetireSources(ctx, configured)
	// What was withdrawn before a failure is reported too; the next
	// publication retires the rest.
	for _, release := range retired.Releases {
		logger.InfoContext(ctx, "catalog retired a release of a removed source", "source", release.Source, "slug", release.Slug, "version", release.Version)
	}
	for _, digest := range retired.Archives {
		logger.InfoContext(ctx, "catalog removed an archive only a removed source referenced", "sha256", digest)
	}
	if err != nil {
		return err
	}
	return errors.Join(failures...)
}
