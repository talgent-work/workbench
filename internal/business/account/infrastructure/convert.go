package infrastructure

import (
	"time"

	"github.com/talgent-work/workbench/internal/business/account/application/query"
	"github.com/talgent-work/workbench/internal/business/account/domain"
)

func millis(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func fromMillis(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}

func accountToDO(account *domain.Account) *accountDO {
	return &accountDO{
		ID:             string(account.ID),
		GitHubUserID:   int64(account.GitHubBinding),
		Username:       account.Profile.Username,
		Email:          account.Profile.Email,
		DisplayName:    account.Profile.DisplayName,
		AvatarURL:      account.Profile.AvatarURL,
		LastSignedInAt: millis(account.LastSignedInAt),
		Version:        account.Version,
		CreatedAt:      millis(account.CreatedAt),
	}
}

func accountFromDO(row *accountDO) (*domain.Account, error) {
	account := &domain.Account{
		ID:            domain.AccountID(row.ID),
		GitHubBinding: domain.GitHubBinding(row.GitHubUserID),
		Profile: domain.Profile{
			Username:    row.Username,
			Email:       row.Email,
			DisplayName: row.DisplayName,
			AvatarURL:   row.AvatarURL,
		},
		CreatedAt:      fromMillis(row.CreatedAt),
		LastSignedInAt: fromMillis(row.LastSignedInAt),
		Version:        row.Version,
	}
	if err := account.Validate(); err != nil {
		return nil, err
	}
	return account, nil
}

func directoryUserFromDO(row *accountDO) query.User {
	return query.User{
		ID:          row.ID,
		Username:    row.Username,
		Email:       row.Email,
		DisplayName: row.DisplayName,
		AvatarURL:   row.AvatarURL,
	}
}

func loginSessionToDO(session *domain.LoginSession) *loginSessionDO {
	return &loginSessionDO{
		ID:                session.ID,
		AccountID:         string(session.AccountReference),
		RefreshDigest:     string(session.RefreshSecret),
		ExpiresAt:         millis(session.ExpiresAt),
		AbsoluteExpiresAt: millis(session.AbsoluteExpiresAt),
		RevokedAt:         millis(session.RevokedAt),
		LastSeenAt:        millis(session.LastSeenAt),
		UserAgent:         session.UserAgent,
		Version:           session.Version,
	}
}

func loginSessionFromDO(row *loginSessionDO) (*domain.LoginSession, error) {
	session := &domain.LoginSession{
		ID:                row.ID,
		AccountReference:  domain.AccountID(row.AccountID),
		RefreshSecret:     domain.RefreshSecretDigest(row.RefreshDigest),
		ExpiresAt:         fromMillis(row.ExpiresAt),
		AbsoluteExpiresAt: fromMillis(row.AbsoluteExpiresAt),
		RevokedAt:         fromMillis(row.RevokedAt),
		LastSeenAt:        fromMillis(row.LastSeenAt),
		UserAgent:         row.UserAgent,
		Version:           row.Version,
	}
	if err := session.Validate(); err != nil {
		return nil, err
	}
	return session, nil
}
