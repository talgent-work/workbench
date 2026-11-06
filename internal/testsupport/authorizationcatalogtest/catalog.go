// Package authorizationcatalogtest supplies the external Marketplace boundary
// for tests whose users have no configured personal authorization.
package authorizationcatalogtest

import (
	"context"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

type Empty struct{}

func (Empty) ListPersonalAuthorizations(context.Context, string) ([]domain.PersonalAuthorization, error) {
	return nil, nil
}

func (Empty) ListAccessibleRepositories(context.Context, string) ([]domain.AccessibleRepository, error) {
	return nil, nil
}
