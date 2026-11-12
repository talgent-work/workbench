package domain

import (
	"context"
)

type ProviderAuthorizationRepository interface {
	Save(ctx context.Context, authorization *ProviderAuthorization) error
	Get(ctx context.Context, id string) (*ProviderAuthorization, error)
	GetByOwnerAndProvider(ctx context.Context, ownerID string, provider Provider) (*ProviderAuthorization, error)
}
