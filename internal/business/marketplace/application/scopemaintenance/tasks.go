// Package scopemaintenance keeps each Active authorization's observed access
// scope current: a periodic scan schedules observations of scopes older than a
// day, and Provider notifications schedule the affected ones.
package scopemaintenance

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	componenttaskqueue "github.com/go-jimu/components/taskqueue"

	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/application/query"
)

const (
	TaskTypeReconcileScan componenttaskqueue.TaskType = "scm.provider_access_scope_reconcile_scan.v1"
	TaskTypeObserve       componenttaskqueue.TaskType = "scm.provider_access_scope_observe.v1"

	// ScanInterval is how often the scan looks for stale scopes; Staleness is
	// how old a scope may be before the daily reconciliation observes it again.
	ScanInterval = time.Hour
	Staleness    = 24 * time.Hour
	scanBatch    = 200
)

var ErrInvalidPayload = errors.New("scope maintenance: invalid payload")

type ReconcileScanPayload struct{}

type ObservePayload struct {
	AuthorizationID string `json:"authorization_id"`
}

func (p ObservePayload) Validate() error {
	if strings.TrimSpace(p.AuthorizationID) == "" {
		return ErrInvalidPayload
	}
	return nil
}

func RegisterTaskSchemas(registry *componenttaskqueue.SchemaRegistry, queue string) error {
	if err := registry.Register(componenttaskqueue.Definition{Type: TaskTypeReconcileScan, Queue: queue}, func() any { return &ReconcileScanPayload{} }); err != nil {
		return err
	}
	return registry.Register(componenttaskqueue.Definition{Type: TaskTypeObserve, Queue: queue}, func() any { return &ObservePayload{} })
}

// observationWindow groups the observation requests for one authorization: the
// requests of one window share one task, which runs after the window closes,
// so a request arriving while an observation runs gets a later one.
const observationWindow = 5 * time.Second

// Scheduler enqueues observations, one per authorization and window.
type Scheduler struct {
	enqueuer componenttaskqueue.Enqueuer
	registry *componenttaskqueue.SchemaRegistry
	now      func() time.Time
}

func NewScheduler(enqueuer componenttaskqueue.Enqueuer, registry *componenttaskqueue.SchemaRegistry) *Scheduler {
	return &Scheduler{enqueuer: enqueuer, registry: registry, now: time.Now}
}

func (s *Scheduler) ScheduleAccessScopeObservation(ctx context.Context, authorizationID string) error {
	payload := &ObservePayload{AuthorizationID: authorizationID}
	if err := payload.Validate(); err != nil {
		return err
	}
	now := s.now()
	window := now.Truncate(observationWindow)
	key := fmt.Sprintf("access-scope:%s:%d", authorizationID, window.Unix())
	task, err := s.registry.NewJSONTask(payload, componenttaskqueue.WithID(key))
	if err != nil {
		return err
	}
	return s.enqueuer.Enqueue(ctx, task,
		componenttaskqueue.WithProcessAt(window.Add(observationWindow)),
		componenttaskqueue.WithMaxRetry(5),
		componenttaskqueue.WithTimeout(5*time.Minute),
	)
}

// Observer replaces an authorization's scope with a live observation.
type Observer interface {
	ObserveAccessScope(ctx context.Context, authorizationID string) error
}

type ObserveProcessor struct{ observer Observer }

func NewObserveProcessor(observer Observer) *ObserveProcessor {
	return &ObserveProcessor{observer: observer}
}

func (p *ObserveProcessor) Process(ctx context.Context, payload ObservePayload) error {
	if err := payload.Validate(); err != nil {
		return err
	}
	return p.observer.ObserveAccessScope(ctx, payload.AuthorizationID)
}

// ReconcileScanProcessor is the daily reconciliation: it schedules every
// Active authorization whose scope is older than Staleness, or never observed,
// correcting reach that a lost Provider notification left stale.
type ReconcileScanProcessor struct {
	candidates query.ProviderAuthorizationQueryRepository
	scheduler  application.MarketplaceTaskQueuePort
	now        func() time.Time
}

func NewReconcileScanProcessor(candidates query.ProviderAuthorizationQueryRepository, scheduler application.MarketplaceTaskQueuePort, now func() time.Time) *ReconcileScanProcessor {
	if now == nil {
		now = time.Now
	}
	return &ReconcileScanProcessor{candidates: candidates, scheduler: scheduler, now: now}
}

func (p *ReconcileScanProcessor) Process(ctx context.Context) error {
	ids, err := p.candidates.ListScopeObservationCandidates(ctx, p.now().Add(-Staleness), scanBatch)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := p.scheduler.ScheduleAccessScopeObservation(ctx, id); err != nil {
			return err
		}
	}
	return nil
}
