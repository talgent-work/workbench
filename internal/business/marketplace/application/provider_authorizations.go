package application

import (
	"context"
	"strings"

	"github.com/talgent-work/workbench/internal/business/marketplace/application/query"
)

func (a *Application) ListPersonalAuthorizations(ctx context.Context, actor string) ([]query.PersonalAuthorization, error) {
	if strings.TrimSpace(actor) == "" {
		return nil, ErrUnauthenticated
	}
	return a.authorizations.ListPersonalAuthorizations(ctx, actor)
}
