package tokenmaintenance

import (
	"context"
	"strings"
)

type RefreshAuthorizationProcessor struct {
	refresher AuthorizationRefresher
}

func NewRefreshAuthorizationProcessor(refresher AuthorizationRefresher) *RefreshAuthorizationProcessor {
	return &RefreshAuthorizationProcessor{refresher: refresher}
}

func (p *RefreshAuthorizationProcessor) Process(ctx context.Context, payload RefreshAuthorizationPayload) error {
	if err := payload.Validate(); err != nil {
		return err
	}
	reason := strings.TrimSpace(payload.Reason)
	if reason == "" {
		reason = ReasonAccessExpiring
	}
	if err := p.refresher.RefreshProviderAuthorization(ctx, payload.AuthorizationID, reason); err != nil {
		return err
	}
	return nil
}
