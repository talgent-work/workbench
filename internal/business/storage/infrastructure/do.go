package infrastructure

type directoryDO struct {
	OwnerKey   string `xorm:"owner_key pk"`
	Name       string `xorm:"name pk"`
	Backend    string `xorm:"backend"`
	Lifetime   string `xorm:"lifetime"`
	State      string `xorm:"state"`
	SpaceToken string `xorm:"space_token"`
	WorkID     string `xorm:"work_id"`
	Version    int    `xorm:"'version'"`
	CreatedAt  int64  `xorm:"created_at"`
	UpdatedAt  int64  `xorm:"updated_at"`
	DeletedAt  int64  `xorm:"deleted_at"`
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
