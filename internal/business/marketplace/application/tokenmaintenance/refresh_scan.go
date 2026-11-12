package tokenmaintenance

import (
	"context"
	"errors"
	"time"

	componenttaskqueue "github.com/go-jimu/components/taskqueue"

	"github.com/talgent-work/workbench/internal/business/marketplace/application/query"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

type RefreshScanProcessor struct {
	authorizations domain.ProviderAuthorizationRepository
	candidates     query.ProviderAuthorizationQueryRepository
	enqueuer       componenttaskqueue.Enqueuer
	registry       *componenttaskqueue.SchemaRegistry
	cfg            Config
	now            func() time.Time
}

func NewRefreshScanProcessor(
	authorizations domain.ProviderAuthorizationRepository,
	candidates query.ProviderAuthorizationQueryRepository,
	enqueuer componenttaskqueue.Enqueuer,
	registry *componenttaskqueue.SchemaRegistry,
	cfg Config,
	now func() time.Time,
) *RefreshScanProcessor {
	if now == nil {
		now = time.Now
	}
	normalized, err := cfg.Normalize()
	if err != nil {
		normalized = Config{
			ScanInterval:           Duration(defaultScanInterval),
			ProactiveRefreshWindow: Duration(defaultProactiveRefreshWindow),
			BatchSize:              defaultBatchSize,
			QueueName:              defaultQueueName,
		}
	}
	return &RefreshScanProcessor{
		authorizations: authorizations,
		candidates:     candidates,
		enqueuer:       enqueuer,
		registry:       registry,
		cfg:            normalized,
		now:            now,
	}
}

func (p *RefreshScanProcessor) Process(ctx context.Context) error {
	now := p.now().UTC()
	window := p.cfg.ProactiveRefreshWindow.Duration()
	candidates, err := p.candidates.ListRefreshCandidates(ctx, now.Add(window), now, p.cfg.BatchSize)
	if err != nil {
		return err
	}
	for _, id := range candidates {
		auth, err := p.authorizations.Get(ctx, id)
		if errors.Is(err, domain.ErrProviderNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if auth.Status != domain.ProviderAuthorizationActive {
			continue
		}
		if auth.NeedsRefresh(now, window) {
			deadlineUnixMS := now.Add(window).UnixMilli()
			if !auth.ExpiresAt.IsZero() {
				deadlineUnixMS = auth.ExpiresAt.UTC().UnixMilli()
			}
			if err := p.enqueue(ctx, &RefreshAuthorizationPayload{
				AuthorizationID: auth.ID,
				Attempt:         0,
				DeadlineUnixMS:  deadlineUnixMS,
				Reason:          ReasonAccessExpiring,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p *RefreshScanProcessor) enqueue(ctx context.Context, payload any) error {
	task, err := p.registry.NewJSONTask(payload)
	if err != nil {
		return err
	}
	var authorizationID string
	switch typed := payload.(type) {
	case *RefreshAuthorizationPayload:
		authorizationID = typed.AuthorizationID
	}
	return p.enqueuer.Enqueue(ctx, task, AuthorizationTaskEnqueueOptions(authorizationID, p.cfg)...)
}
