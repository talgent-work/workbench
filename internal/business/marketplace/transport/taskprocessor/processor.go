package taskprocessor

import (
	"context"
	"errors"
	"fmt"

	componenttaskqueue "github.com/go-jimu/components/taskqueue"

	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/application/scopemaintenance"
	"github.com/talgent-work/workbench/internal/business/marketplace/application/tokenmaintenance"
)

type RefreshScanProcessor interface {
	Process(context.Context) error
}

type RefreshAuthorizationProcessor interface {
	Process(context.Context, tokenmaintenance.RefreshAuthorizationPayload) error
}

func RegisterTaskSchemas(registry *componenttaskqueue.SchemaRegistry, cfg tokenmaintenance.Config) error {
	if err := tokenmaintenance.RegisterTaskSchemas(registry, cfg); err != nil {
		return err
	}
	normalized, err := cfg.Normalize()
	if err != nil {
		return err
	}
	return scopemaintenance.RegisterTaskSchemas(registry, normalized.QueueName)
}

func NewRefreshScanTaskProcessor(
	registry *componenttaskqueue.SchemaRegistry,
	processor RefreshScanProcessor,
) componenttaskqueue.Processor {
	return componenttaskqueue.NewProcessor(tokenmaintenance.TaskTypeRefreshScan, func(ctx context.Context, task componenttaskqueue.Task) error {
		decoded, err := registry.DecodeJSON(task)
		if err != nil {
			return err
		}
		if _, ok := decoded.(*tokenmaintenance.RefreshScanPayload); !ok {
			return fmt.Errorf("%w: unexpected refresh scan payload %T", componenttaskqueue.ErrSkipRetry, decoded)
		}
		return mapProcessorError(processor.Process(ctx))
	})
}

func NewRefreshAuthorizationTaskProcessor(
	registry *componenttaskqueue.SchemaRegistry,
	processor RefreshAuthorizationProcessor,
) componenttaskqueue.Processor {
	return componenttaskqueue.NewProcessor(tokenmaintenance.TaskTypeRefreshAuthorization, func(ctx context.Context, task componenttaskqueue.Task) error {
		decoded, err := registry.DecodeJSON(task)
		if err != nil {
			return err
		}
		payload, ok := decoded.(*tokenmaintenance.RefreshAuthorizationPayload)
		if !ok {
			return fmt.Errorf("%w: unexpected refresh authorization payload %T", componenttaskqueue.ErrSkipRetry, decoded)
		}
		return mapProcessorError(processor.Process(ctx, *payload))
	})
}

func mapProcessorError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, tokenmaintenance.ErrInvalidPayload) || errors.Is(err, scopemaintenance.ErrInvalidPayload) {
		return fmt.Errorf("%w: %w", componenttaskqueue.ErrSkipRetry, err)
	}
	var providerErr *application.ProviderError
	if errors.As(err, &providerErr) && !providerErr.Retryable {
		return fmt.Errorf("%w: %w", componenttaskqueue.ErrSkipRetry, err)
	}
	return err
}

type ScopeReconcileScanProcessor interface {
	Process(context.Context) error
}

type ScopeObserveProcessor interface {
	Process(context.Context, scopemaintenance.ObservePayload) error
}

func NewScopeReconcileScanTaskProcessor(registry *componenttaskqueue.SchemaRegistry, processor ScopeReconcileScanProcessor) componenttaskqueue.Processor {
	return componenttaskqueue.NewProcessor(scopemaintenance.TaskTypeReconcileScan, func(ctx context.Context, task componenttaskqueue.Task) error {
		decoded, err := registry.DecodeJSON(task)
		if err != nil {
			return err
		}
		if _, ok := decoded.(*scopemaintenance.ReconcileScanPayload); !ok {
			return fmt.Errorf("%w: unexpected scope reconcile payload %T", componenttaskqueue.ErrSkipRetry, decoded)
		}
		return mapProcessorError(processor.Process(ctx))
	})
}

func NewScopeObserveTaskProcessor(registry *componenttaskqueue.SchemaRegistry, processor ScopeObserveProcessor) componenttaskqueue.Processor {
	return componenttaskqueue.NewProcessor(scopemaintenance.TaskTypeObserve, func(ctx context.Context, task componenttaskqueue.Task) error {
		decoded, err := registry.DecodeJSON(task)
		if err != nil {
			return err
		}
		payload, ok := decoded.(*scopemaintenance.ObservePayload)
		if !ok {
			return fmt.Errorf("%w: unexpected scope observe payload %T", componenttaskqueue.ErrSkipRetry, decoded)
		}
		return mapProcessorError(processor.Process(ctx, *payload))
	})
}
