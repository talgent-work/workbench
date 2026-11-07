package connectrpc

import (
	marketplacepublicv1 "github.com/talgent-work/contracts/gen/marketplace/public/v1"
	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/application/query"
)

func accessibleRepositoryMessages(repositories []application.ProviderRepository) []*marketplacepublicv1.AccessibleRepository {
	result := make([]*marketplacepublicv1.AccessibleRepository, 0, len(repositories))
	for _, r := range repositories {
		result = append(result, &marketplacepublicv1.AccessibleRepository{Id: r.RepoID, Provider: string(r.Provider), Name: r.RepoName, CloneUrl: r.CloneURL, WebUrl: r.WebURL, DefaultBranch: r.DefaultBranch})
	}
	return result
}

func connectionStateMessage(v *query.ProviderConnectionState) *marketplacepublicv1.ProviderConnectionState {
	if v == nil {
		return nil
	}
	return &marketplacepublicv1.ProviderConnectionState{
		AuthorizationId:             v.AuthorizationID,
		Provider:                    v.Provider,
		Status:                      v.Status,
		ProviderAccountId:           v.ProviderAccountID,
		ProviderLogin:               v.ProviderLogin,
		CanListRepositories:         v.CanListRepositories,
		RecommendedAction:           v.RecommendedAction,
		ReasonCode:                  v.ReasonCode,
		ReasonMessage:               v.ReasonMessage,
		AccessTokenExpiresAtUnixMs:  v.AccessTokenExpiresAtUnixMS,
		RefreshTokenExpiresAtUnixMs: v.RefreshTokenExpiresAtUnixMS,
		LastCheckedAtUnixMs:         v.LastCheckedAtUnixMS,
		LastFailureCode:             v.LastFailureCode,
	}
}
