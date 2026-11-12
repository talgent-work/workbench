package infrastructure

type accountDO struct {
	ID             string `xorm:"id pk"`
	GitHubUserID   int64  `xorm:"github_user_id"`
	Username       string `xorm:"username"`
	Email          string `xorm:"email"`
	DisplayName    string `xorm:"display_name"`
	AvatarURL      string `xorm:"avatar_url"`
	LastSignedInAt int64  `xorm:"last_signed_in_at"`
	Version        int    `xorm:"'version'"`
	CreatedAt      int64  `xorm:"created_at"`
	UpdatedAt      int64  `xorm:"updated_at"`
	DeletedAt      int64  `xorm:"deleted_at"`
}

func (accountDO) TableName() string { return "account" }

type loginSessionDO struct {
	ID                string `xorm:"id pk"`
	AccountID         string `xorm:"account_id"`
	RefreshDigest     string `xorm:"refresh_digest"`
	ExpiresAt         int64  `xorm:"expires_at"`
	AbsoluteExpiresAt int64  `xorm:"absolute_expires_at"`
	RevokedAt         int64  `xorm:"revoked_at"`
	LastSeenAt        int64  `xorm:"last_seen_at"`
	UserAgent         string `xorm:"user_agent"`
	Version           int    `xorm:"'version'"`
	CreatedAt         int64  `xorm:"created_at"`
	UpdatedAt         int64  `xorm:"updated_at"`
	DeletedAt         int64  `xorm:"deleted_at"`
}

func (loginSessionDO) TableName() string { return "login_session" }

type replacedSecretDO struct {
	ID            string `xorm:"id pk"`
	SessionID     string `xorm:"session_id"`
	RefreshDigest string `xorm:"refresh_digest"`
	ExpiresAt     int64  `xorm:"expires_at"`
	Version       int    `xorm:"'version'"`
	CreatedAt     int64  `xorm:"created_at"`
	UpdatedAt     int64  `xorm:"updated_at"`
	DeletedAt     int64  `xorm:"deleted_at"`
}

func (replacedSecretDO) TableName() string { return "login_session_replaced_secret" }

type signingKeyDO struct {
	ID               string `xorm:"id pk"`
	Generation       uint32 `xorm:"generation"`
	Algorithm        string `xorm:"algorithm"`
	PublicJWK        string `xorm:"public_jwk"`
	SealedPrivateKey []byte `xorm:"sealed_private_key"`
	ActivatesAt      int64  `xorm:"activates_at"`
	RetiresAt        int64  `xorm:"retires_at"`
	Version          int    `xorm:"'version'"`
	CreatedAt        int64  `xorm:"created_at"`
	UpdatedAt        int64  `xorm:"updated_at"`
	DeletedAt        int64  `xorm:"deleted_at"`
}

func (signingKeyDO) TableName() string { return "signing_key" }

type agentAccountDO struct {
	WorkID            string `xorm:"work_id pk"`
	ProjectID         string `xorm:"project_id"`
	AgentID           string `xorm:"agent_id"`
	Purpose           string `xorm:"purpose"`
	WorkSubjectKind   string `xorm:"work_subject_kind"`
	WorkSubjectID     string `xorm:"work_subject_id"`
	RepresentedUserID string `xorm:"represented_user_id"`
	ApplicationID     string `xorm:"application_id"`
	Version           int    `xorm:"'version'"`
	CreatedAt         int64  `xorm:"created_at"`
	UpdatedAt         int64  `xorm:"updated_at"`
	DeletedAt         int64  `xorm:"deleted_at"`
}

func (agentAccountDO) TableName() string { return "agent_account" }

type applicationDO struct {
	ID           string `xorm:"id pk"`
	OwnerID      string `xorm:"owner_id"`
	Name         string `xorm:"name"`
	State        int    `xorm:"state"`
	RegisteredAt int64  `xorm:"registered_at"`
	Version      int    `xorm:"'version'"`
	CreatedAt    int64  `xorm:"created_at"`
	UpdatedAt    int64  `xorm:"updated_at"`
	DeletedAt    int64  `xorm:"deleted_at"`
}

func (applicationDO) TableName() string { return "application" }

type clientSecretDO struct {
	ID            string `xorm:"id pk"`
	ApplicationID string `xorm:"application_id"`
	SecretDigest  string `xorm:"secret_digest"`
	Hint          string `xorm:"hint"`
	State         int    `xorm:"state"`
	IssuedAt      int64  `xorm:"issued_at"`
	OverlapEndsAt int64  `xorm:"overlap_ends_at"`
	LastUsedAt    int64  `xorm:"last_used_at"`
	Version       int    `xorm:"'version'"`
	CreatedAt     int64  `xorm:"created_at"`
	UpdatedAt     int64  `xorm:"updated_at"`
	DeletedAt     int64  `xorm:"deleted_at"`
}

func (clientSecretDO) TableName() string { return "application_client_secret" }
