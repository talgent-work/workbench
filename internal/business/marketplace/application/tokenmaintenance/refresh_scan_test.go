package tokenmaintenance

import (
	"context"
	"errors"
	"testing"
	"time"

	componenttaskqueue "github.com/go-jimu/components/taskqueue"

	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/application/query"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

// The scan must enqueue only active authorizations whose access token expires
// inside the proactive window, and task payloads must carry identifiers only.
func TestRefreshScanProcessor_EnqueuesOnlyActiveNearExpiryAuthorizations(t *testing.T) {
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	near := testAuthorization("auth-near", "user-1", now.Add(20*time.Minute), domain.ProviderAuthorizationActive)
	near.LastCheckedAt = now.Add(-10 * time.Minute)
	far := testAuthorization("auth-far", "user-1", now.Add(2*time.Hour), domain.ProviderAuthorizationActive)
	far.LastCheckedAt = now.Add(-10 * time.Minute)
	reconnect := testAuthorization("auth-reconnect", "user-1", now.Add(20*time.Minute), domain.ProviderAuthorizationReconnectRequired)
	reconnect.LastCheckedAt = now.Add(-10 * time.Minute)
	auths := &fakeAuthorizationRepository{
		refreshCandidates: []*domain.ProviderAuthorization{near, far, reconnect},
	}
	enqueuer := &fakeMaintenanceEnqueuer{}
	registry := newMaintenanceTaskRegistry(t)
	processor := NewRefreshScanProcessor(auths, auths, enqueuer, registry, Config{
		ProactiveRefreshWindow: Duration(30 * time.Minute),
		BatchSize:              100,
	}, func() time.Time { return now })

	if err := processor.Process(context.Background()); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if !auths.listCutoff.Equal(now.Add(30 * time.Minute)) {
		t.Fatalf("candidate cutoff=%s, want %s", auths.listCutoff, now.Add(30*time.Minute))
	}
	if got := len(enqueuer.refreshes); got != 1 {
		t.Fatalf("refresh tasks=%d, want 1: %+v", got, enqueuer.refreshes)
	}
	payload := enqueuer.refreshes[0]
	if payload.AuthorizationID != "auth-near" || payload.Reason != ReasonAccessExpiring {
		t.Fatalf("payload=%+v", payload)
	}
	if payload.DeadlineUnixMS == 0 {
		t.Fatalf("deadline missing in payload: %+v", payload)
	}
}

func TestRefreshScanProcessorRechecksCurrentAuthorizationAfterCandidateSelection(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	for _, state := range []string{"renewed", "revoked", "reconnect_required", "removed"} {
		t.Run(state, func(t *testing.T) {
			selected := testAuthorization("candidate", "owner", now, domain.ProviderAuthorizationActive)
			repository := &fakeAuthorizationRepository{
				refreshCandidates: []*domain.ProviderAuthorization{selected},
				byID:              map[string]*domain.ProviderAuthorization{},
			}
			if state != "removed" {
				current := testAuthorization("candidate", "owner", now, domain.ProviderAuthorizationActive)
				switch state {
				case "renewed":
					current.ExpiresAt = now.Add(2 * time.Hour)
					current.LastCheckedAt = now
				case "revoked":
					if err := current.MarkRevoked(now); err != nil {
						t.Fatal(err)
					}
				case "reconnect_required":
					if err := current.MarkReconnectRequired("invalid_grant", "reauthorization required", now); err != nil {
						t.Fatal(err)
					}
				}
				repository.byID[current.ID] = current
			}
			enqueuer := &fakeMaintenanceEnqueuer{}
			processor := NewRefreshScanProcessor(repository, repository, enqueuer, newMaintenanceTaskRegistry(t), Config{}, func() time.Time { return now })
			if err := processor.Process(t.Context()); err != nil {
				t.Fatal(err)
			}
			if len(enqueuer.refreshes) != 0 {
				t.Fatalf("outdated candidate scheduled maintenance after %s", state)
			}
		})
	}
}

func TestRefreshScanProcessorPropagatesReadFailuresWithoutScheduling(t *testing.T) {
	for _, stage := range []string{"query", "load"} {
		t.Run(stage, func(t *testing.T) {
			now := time.Now().UTC()
			failure := errors.New("temporary persistence failure")
			repository := &fakeAuthorizationRepository{refreshCandidates: []*domain.ProviderAuthorization{
				testAuthorization("candidate", "owner", now, domain.ProviderAuthorizationActive),
			}}
			if stage == "query" {
				repository.queryError = failure
			} else {
				repository.loadError = failure
			}
			enqueuer := &fakeMaintenanceEnqueuer{}
			processor := NewRefreshScanProcessor(repository, repository, enqueuer, newMaintenanceTaskRegistry(t), Config{}, func() time.Time { return now })
			if err := processor.Process(t.Context()); !errors.Is(err, failure) {
				t.Fatalf("lost read failure: %v", err)
			}
			if len(enqueuer.refreshes) != 0 {
				t.Fatal("scheduled from an unverified candidate")
			}
		})
	}
}

// Refresh processing must use the same force-refresh path exposed by the
// provider adapter and preserve provider retryability for the task adapter.
func TestRefreshAuthorizationProcessor_PropagatesProviderRetryability(t *testing.T) {
	permanent := application.NewProviderError("github_reconnect_required", false, "reconnect", application.ErrProviderUnavailable)
	retryable := application.NewProviderError("github_provider_transient", true, "github unavailable", errors.New("timeout"))

	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "permanent", err: permanent},
		{name: "retryable", err: retryable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			refresher := &fakeAuthorizationRefresher{err: tc.err}
			processor := NewRefreshAuthorizationProcessor(refresher)

			err := processor.Process(context.Background(), RefreshAuthorizationPayload{AuthorizationID: "auth-1"})
			if !errors.Is(err, tc.err) {
				t.Fatalf("Process err=%v, want wrapped/same %v", err, tc.err)
			}
			if refresher.authorizationID != "auth-1" || refresher.reason != ReasonAccessExpiring {
				t.Fatalf("refresh call id=%q reason=%q", refresher.authorizationID, refresher.reason)
			}
		})
	}
}

// A successful refresh task is complete; retrying it would rotate tokens again.
func TestRefreshAuthorizationProcessor_CompletesAfterSuccessfulRefresh(t *testing.T) {
	refresher := &fakeAuthorizationRefresher{}
	processor := NewRefreshAuthorizationProcessor(refresher)

	if err := processor.Process(context.Background(), RefreshAuthorizationPayload{AuthorizationID: "auth-1"}); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if refresher.authorizationID != "auth-1" {
		t.Fatalf("refresh call id=%q, want auth-1", refresher.authorizationID)
	}
}

type fakeAuthorizationRepository struct {
	// The selections the refresh scan does not use stay unimplemented.
	query.ProviderAuthorizationQueryRepository
	queryError, loadError error
	refreshCandidates     []*domain.ProviderAuthorization
	listCutoff            time.Time
	leaseCutoff           time.Time
	byID                  map[string]*domain.ProviderAuthorization
}

func (r *fakeAuthorizationRepository) Save(context.Context, *domain.ProviderAuthorization) error {
	return nil
}

func (r *fakeAuthorizationRepository) Get(_ context.Context, id string) (*domain.ProviderAuthorization, error) {
	if r.loadError != nil {
		return nil, r.loadError
	}
	auth, ok := r.byID[id]
	if r.byID == nil {
		for _, candidate := range r.refreshCandidates {
			if candidate != nil && candidate.ID == id {
				return candidate, nil
			}
		}
	}
	if !ok {
		return nil, domain.ErrProviderNotFound
	}
	return auth, nil
}

func (r *fakeAuthorizationRepository) GetByOwnerAndProvider(context.Context, string, domain.Provider) (*domain.ProviderAuthorization, error) {
	return nil, domain.ErrProviderNotFound
}

func (r *fakeAuthorizationRepository) ListScopeObservationCandidates(context.Context, time.Time, int) ([]string, error) {
	return nil, nil
}

func (r *fakeAuthorizationRepository) ListRefreshCandidates(_ context.Context, refreshCutoff, leaseCutoff time.Time, _ int) ([]string, error) {
	if r.queryError != nil {
		return nil, r.queryError
	}
	r.listCutoff = refreshCutoff
	r.leaseCutoff = leaseCutoff
	ids := make([]string, 0, len(r.refreshCandidates))
	for _, candidate := range r.refreshCandidates {
		if candidate != nil {
			ids = append(ids, candidate.ID)
		}
	}
	return ids, nil
}

func (r *fakeAuthorizationRepository) AcquireCredentialLease(context.Context, *domain.ProviderAuthorization, string, time.Time, time.Time) (*domain.ProviderAuthorization, error) {
	return nil, domain.ErrConcurrentModification
}

type fakeMaintenanceEnqueuer struct {
	refreshes []RefreshAuthorizationPayload
	err       error
}

func (e *fakeMaintenanceEnqueuer) Enqueue(_ context.Context, task componenttaskqueue.Task, _ ...componenttaskqueue.EnqueueOption) error {
	if e.err != nil {
		return e.err
	}
	switch task.Type() {
	case TaskTypeRefreshAuthorization:
		var payload RefreshAuthorizationPayload
		if err := componenttaskqueue.DecodeJSON(task, &payload); err != nil {
			return err
		}
		e.refreshes = append(e.refreshes, payload)
	}
	return nil
}

func newMaintenanceTaskRegistry(t *testing.T) *componenttaskqueue.SchemaRegistry {
	t.Helper()
	registry := componenttaskqueue.NewSchemaRegistry()
	cfg, err := Config{}.Normalize()
	if err != nil {
		t.Fatalf("normalize config: %v", err)
	}
	if err := RegisterTaskSchemas(registry, cfg); err != nil {
		t.Fatalf("register task schemas: %v", err)
	}
	return registry
}

type fakeAuthorizationRefresher struct {
	// Only renewal is exercised; GitHub's other operations stay unimplemented.
	application.GitHubPort
	authorizationID string
	reason          string
	err             error
}

func (r *fakeAuthorizationRefresher) RefreshProviderAuthorization(_ context.Context, authorizationID string, reason string) error {
	r.authorizationID = authorizationID
	r.reason = reason
	return r.err
}

func testAuthorization(id, owner string, expiresAt time.Time, status domain.ProviderAuthorizationStatus) *domain.ProviderAuthorization {
	return &domain.ProviderAuthorization{
		ID:       id,
		OwnerID:  owner,
		Provider: domain.ProviderGitHub,
		Status:   status,
		Version:  1,

		ExternalAccount: domain.ExternalAccount{
			ProviderAccountID: owner + "-github",
			ProviderLogin:     owner,
		},

		CredentialSet: domain.CredentialSet{
			AccessTokenCiphertext:  "access",
			RefreshTokenCiphertext: "refresh",
			ExpiresAt:              expiresAt,
			RefreshTokenExpiresAt:  expiresAt.Add(time.Hour),
		},
	}
}
