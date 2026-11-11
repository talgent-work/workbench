package infrastructure

type directoryDO struct {
	OwnerKey   string `xorm:"owner_key pk"`
	Name       string `xorm:"name pk"`
	Backend    string `xorm:"backend"`
	Lifetime   string `xorm:"lifetime"`
	State      string `xorm:"state"`
	SpaceToken string `xorm:"space_token"`
	// GrantedReadWrite is 1 once a read-write Mount Grant was issued.
	GrantedReadWrite uint8  `xorm:"granted_read_write"`
	WorkID           string `xorm:"work_id"`
	Version          int    `xorm:"'version'"`
	CreatedAt        int64  `xorm:"created_at"`
	UpdatedAt        int64  `xorm:"updated_at"`
	DeletedAt        int64  `xorm:"deleted_at"`
}

func (directoryDO) TableName() string { return "storage_directory" }

type shareLinkDO struct {
	Token         string `xorm:"token pk"`
	OwnerKey      string `xorm:"owner_key"`
	DirectoryName string `xorm:"directory_name"`
	TargetName    string `xorm:"target_name"`
	CreatedBy     string `xorm:"created_by"`
	State         string `xorm:"state"`
	Version       int    `xorm:"'version'"`
	CreatedAt     int64  `xorm:"created_at"`
	UpdatedAt     int64  `xorm:"updated_at"`
	DeletedAt     int64  `xorm:"deleted_at"`
}

func (shareLinkDO) TableName() string { return "storage_share_link" }

// mappingDO is one Directory Mapping; its row exists while the mapping does.
type mappingDO struct {
	Path          string `xorm:"path pk"`
	OwnerKey      string `xorm:"owner_key"`
	DirectoryName string `xorm:"directory_name"`
	CreatedAt     int64  `xorm:"created_at"`
}

func (mappingDO) TableName() string { return "storage_directory_mapping" }
