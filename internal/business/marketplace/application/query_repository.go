package application

import (
	"context"

	"github.com/talgent-work/workbench/internal/business/marketplace/application/query"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

type ProviderConnectionQueryRepository interface {
	GetProviderConnectionState(ctx context.Context, ownerUserID string, provider domain.Provider) (*query.ProviderConnectionState, error)
}
