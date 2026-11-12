package query

import (
	"context"
	"time"
)

// WorkQueryRepository reads Works without loading the AgentWork: their
// views, lists, origins and lifecycle events, first-start preparations,
// recent repositories, Standalone Works and Library, history reports, and the
// facts Work access is decided from.
type WorkQueryRepository interface {
	// GetWorkView reads the Work's lifecycle, its current start and its
	// current execution together, so the view never combines facts of
	// different starts.
	GetWorkView(ctx context.Context, workID string) (WorkView, error)
	ListWorkViews(ctx context.Context, filter WorkListFilter) ([]WorkSummary, error)
	GetWorkOrigin(ctx context.Context, workID string) (WorkOrigin, error)
	// ListWorkOrigins reads the origins of several Works at once; a Work
	// that does not exist is absent from the result.
	ListWorkOrigins(ctx context.Context, workIDs []string) (map[string]WorkOrigin, error)
	ListWorkLifecycleEvents(ctx context.Context, workID string) ([]WorkLifecycleEvent, error)
	GetWorkPreparation(ctx context.Context, workID string) (WorkPreparation, bool, error)
	ListSubmittedPreparations(ctx context.Context, after string, limit int) ([]WorkPreparation, error)
	// ListRecentRepositoryIDs reads repository identities from the owner's
	// own Works, most recently created first and without duplicates.
	ListRecentRepositoryIDs(ctx context.Context, owner string) ([]string, error)
	// ListStandaloneWorks lists a definition's Works without a Subject or a
	// Project, most recently updated first, narrowed as the filter states.
	ListStandaloneWorks(ctx context.Context, filter StandaloneWorksFilter) ([]StandaloneWorkSummary, error)
	// ListWorkAttributions reads those of the named Works that are the
	// definition's Standalone Works, with their Work Labels.
	ListWorkAttributions(ctx context.Context, agentID string, workIDs []string) ([]WorkAttribution, error)
	// ListLibraryWorks lists the person's Standalone Works under their
	// definition and the Works with an input source they hold a Work Share
	// in, closed ones included, at most limit.
	ListLibraryWorks(ctx context.Context, person, agentID string, limit int) ([]LibraryWork, error)
	// ReadWorkHistory reads the Works in scope created before the instant,
	// each with its state history observed before it and its current state.
	ReadWorkHistory(ctx context.Context, scope WorkReportScope, before time.Time) ([]WorkHistory, error)
	// WorkOwners reads the Owners of the Work's definition; false for an
	// unknown Work.
	WorkOwners(ctx context.Context, workID string) ([]string, bool, error)
}
