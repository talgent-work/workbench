package application

import (
	"context"
	"errors"
	"fmt"
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
	// Failure means source enumeration failed; preserve the entire source.
	Failure error
}

type CatalogPublicationPort interface {
	CurrentSource(context.Context, string) ([]PluginRelease, error)
	PublishedRelease(context.Context, string, string, string) (*PluginRelease, error)
	RetainRelease(context.Context, PluginRelease) error
	CommitCatalog(context.Context, []SourceReleases) error
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
// selected view visible in one commit. A partial failure is reported after commit.
func PublishCatalog(ctx context.Context, store CatalogPublicationPort, sources []SourceReleases) error {
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
	return errors.Join(failures...)
}
