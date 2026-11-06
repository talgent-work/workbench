package infrastructure

type computerDO struct {
	ID                     string `xorm:"id pk varchar(36)"`
	Kind                   string `xorm:"kind varchar(16)"`
	OwnerID                string `xorm:"owner_id varchar(255)"`
	Name                   string `xorm:"name varchar(100)"`
	Capacity               string `xorm:"capacity json"`
	Occupied               string `xorm:"occupied json"`
	AdmissionPolicy        string `xorm:"admission_policy varchar(16)"`
	WaitingLine            string `xorm:"waiting_line json"`
	Reachable              bool   `xorm:"reachable"`
	UnreachableSince       int64  `xorm:"unreachable_since bigint"`
	TalgentComputerVersion string `xorm:"talgent_computer_version varchar(64)"`
	ClaudeCodeVersion      string `xorm:"claude_code_version varchar(64)"`
	ClaudeCodeSandbox      bool   `xorm:"claude_code_sandbox"`
	CredentialDigest       string `xorm:"credential_digest varchar(64)"`
	CredentialExpiresAt    int64  `xorm:"credential_expires_at bigint"`
	PendingDigest          string `xorm:"pending_credential_digest varchar(64)"`
	PendingExpiresAt       int64  `xorm:"pending_credential_expires_at bigint"`
	PendingIssuedAt        int64  `xorm:"pending_credential_issued_at bigint"`
	RetiredDigest          string `xorm:"retired_credential_digest varchar(64)"`
	RetiredExpiresAt       int64  `xorm:"retired_credential_expires_at bigint"`
	State                  string `xorm:"state varchar(16)"`
	Version                int    `xorm:"'version' int"`
	CreatedAt              int64  `xorm:"created_at bigint"`
	UpdatedAt              int64  `xorm:"updated_at bigint"`
	DeletedAt              int64  `xorm:"deleted_at bigint"`
}

func (computerDO) TableName() string { return "computer" }

type deskDO struct {
	ID         string `xorm:"id pk varchar(36)"`
	WorkID     string `xorm:"work_id varchar(36)"`
	ComputerID string `xorm:"computer_id varchar(36)"`
	Demand     string `xorm:"demand json"`
	State      string `xorm:"state varchar(16)"`
	Since      int64  `xorm:"since bigint"`
	OpenedAt   int64  `xorm:"opened_at bigint"`
	Version    int    `xorm:"'version' int"`
	CreatedAt  int64  `xorm:"created_at bigint"`
	UpdatedAt  int64  `xorm:"updated_at bigint"`
	DeletedAt  int64  `xorm:"deleted_at bigint"`
}

func (deskDO) TableName() string { return "computer_desk" }

// roomRecord is a Room as stored.
type roomRecord struct {
	MilliCPU    int64 `json:"milli_cpu"`
	MemoryBytes int64 `json:"memory_bytes"`
	Desks       int64 `json:"desks"`
}

type waitingDeskRecord struct {
	DeskID string     `json:"desk_id"`
	Demand roomRecord `json:"demand"`
}
