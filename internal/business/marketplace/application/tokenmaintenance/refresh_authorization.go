package tokenmaintenance

import (
	"context"
)

// AuthorizationRenewal renews one authorization near its credential expiry,
// whichever Provider declares it.
type AuthorizationRenewal interface {
	RenewAuthorization(ctx context.Context, authorizationID string) error
}

type RefreshAuthorizationProcessor struct {
	renewal AuthorizationRenewal
}

func NewRefreshAuthorizationProcessor(renewal AuthorizationRenewal) *RefreshAuthorizationProcessor {
	return &RefreshAuthorizationProcessor{renewal: renewal}
}

func (p *RefreshAuthorizationProcessor) Process(ctx context.Context, payload RefreshAuthorizationPayload) error {
	if err := payload.Validate(); err != nil {
		return err
	}
	return p.renewal.RenewAuthorization(ctx, payload.AuthorizationID)
}
