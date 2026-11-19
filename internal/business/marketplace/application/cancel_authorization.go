package application

import (
	"context"
	"strings"

	"github.com/talgent-work/workbench/internal/business/marketplace/application/query"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

func (a *PersonalConnections) CancelPersonalAuthorization(ctx context.Context, actor, authorizationID string) (query.PersonalAuthorization, error) {
	if strings.TrimSpace(actor) == "" {
		return query.PersonalAuthorization{}, ErrUnauthenticated
	}
	if strings.TrimSpace(authorizationID) == "" {
		return query.PersonalAuthorization{}, ErrInvalidQuery
	}
	auth, err := a.authorizations.Get(ctx, authorizationID)
	if err != nil {
		return query.PersonalAuthorization{}, err
	}
	if auth.OwnerID != actor {
		return query.PersonalAuthorization{}, domain.ErrProviderNotFound
	}
	if a.credentials == nil {
		return query.PersonalAuthorization{}, ErrProviderUnavailable
	}
	if auth, err = a.credentials.Revoke(ctx, actor, auth); err != nil {
		return query.PersonalAuthorization{}, err
	}
	return query.PersonalAuthorization{ID: auth.ID, Provider: string(auth.Provider), Status: string(auth.Status)}, nil
}
