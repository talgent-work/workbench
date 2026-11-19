package application

import (
	"context"
	"strings"

	"github.com/talgent-work/workbench/internal/business/marketplace/application/query"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

// ListPersonalAuthorizations lists the owner's authorization references, each
// with the targets its declared Provider serves; a Provider the deployment no
// longer declares serves none.
func (a *Application) ListPersonalAuthorizations(ctx context.Context, actor string) ([]query.PersonalAuthorization, error) {
	if strings.TrimSpace(actor) == "" {
		return nil, ErrUnauthenticated
	}
	authorizations, err := a.authorizations.ListPersonalAuthorizations(ctx, actor)
	if err != nil {
		return nil, err
	}
	for i := range authorizations {
		authorizations[i].SupportedTargets = nil
		if a.PersonalConnections == nil || a.providers == nil {
			continue
		}
		if declaration, ok := a.providers.Provider(domain.Provider(authorizations[i].Provider)); ok {
			authorizations[i].SupportedTargets = declaration.TargetNames()
		}
	}
	return authorizations, nil
}
