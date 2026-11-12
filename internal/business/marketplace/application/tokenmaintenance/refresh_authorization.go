package tokenmaintenance

import (
	"context"
	"strings"

	"github.com/talgent-work/workbench/internal/business/marketplace/application"
)

type RefreshAuthorizationProcessor struct {
	github application.GitHubPort
}

func NewRefreshAuthorizationProcessor(github application.GitHubPort) *RefreshAuthorizationProcessor {
	return &RefreshAuthorizationProcessor{github: github}
}

func (p *RefreshAuthorizationProcessor) Process(ctx context.Context, payload RefreshAuthorizationPayload) error {
	if err := payload.Validate(); err != nil {
		return err
	}
	reason := strings.TrimSpace(payload.Reason)
	if reason == "" {
		reason = ReasonAccessExpiring
	}
	if err := p.github.RefreshProviderAuthorization(ctx, payload.AuthorizationID, reason); err != nil {
		return err
	}
	return nil
}
