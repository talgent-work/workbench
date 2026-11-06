package persistence

type ProviderAuthorizationDO struct {
	ID                     string `xorm:"id pk varchar(36)"`
	OwnerUserID            string `xorm:"owner_user_id varchar(64) notnull default('')"`
	Provider               string `xorm:"provider varchar(16) notnull default('')"`
	ProviderAccountID      string `xorm:"provider_account_id varchar(128) notnull default('')"`
	ProviderLogin          string `xorm:"provider_login varchar(128) notnull default('')"`
	AccessTokenCiphertext  string `xorm:"access_token_ciphertext text notnull"`
	RefreshTokenCiphertext string `xorm:"refresh_token_ciphertext text notnull"`
	ExpiresAt              int64  `xorm:"expires_at bigint notnull default(0)"`
	RefreshTokenExpiresAt  int64  `xorm:"refresh_token_expires_at bigint notnull default(0)"`
	Status                 string `xorm:"status varchar(32) notnull default('active')"`
	RefreshLeaseID         string `xorm:"refresh_lease_id varchar(64) notnull default('')"`
	RefreshLeaseUntil      int64  `xorm:"refresh_lease_until bigint notnull default(0)"`
	LastRefreshedAt        int64  `xorm:"last_refreshed_at bigint notnull default(0)"`
	LastCheckedAt          int64  `xorm:"last_checked_at bigint notnull default(0)"`
	LastFailureCode        string `xorm:"last_failure_code varchar(128) notnull default('')"`
	LastFailureDetail      string `xorm:"last_failure_detail varchar(512) notnull default('')"`
	AccessScopeJSON        string `xorm:"access_scope_json json"`
	AccessScopeObservedAt  int64  `xorm:"access_scope_observed_at bigint notnull default(0)"`
	Version                int    `xorm:"version notnull default(0)"`
	CreatedAt              int64  `xorm:"created_at bigint notnull default(0)"`
	UpdatedAt              int64  `xorm:"updated_at bigint notnull default(0)"`
	DeletedAt              int64  `xorm:"deleted_at bigint notnull default(0)"`
}

func (ProviderAuthorizationDO) TableName() string { return "scm_provider_authorization" }
