package application

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
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
	now := time.Now().UTC()
	// One minute exceeds the bounded Provider operation and persistence window.
	// Refresh, cancellation and competing workers share this database fence.
	auth, err = a.authorizations.AcquireCredentialLease(ctx, auth, uuid.NewString(), now.Add(time.Minute), now)
	if err != nil {
		return query.PersonalAuthorization{}, err
	}
	operationCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	operationErr := auth.CancelAuthorization(operationCtx, actor, now, a.authorizationPort)
	if err := a.persistCredentialOperation(ctx, auth, operationErr); err != nil {
		return query.PersonalAuthorization{}, err
	}
	return query.PersonalAuthorization{ID: auth.ID, Provider: string(auth.Provider), Status: string(auth.Status)}, nil
}
