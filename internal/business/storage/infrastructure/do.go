package infrastructure

type fileSpaceDO struct {
	Owner      string `xorm:"owner pk"`
	SpaceToken string `xorm:"space_token"`
	Version    int    `xorm:"'version'"`
	CreatedAt  int64  `xorm:"created_at"`
	UpdatedAt  int64  `xorm:"updated_at"`
	DeletedAt  int64  `xorm:"deleted_at"`
}

func (fileSpaceDO) TableName() string { return "storage_file_space" }

type shareLinkDO struct {
	Token      string `xorm:"token pk"`
	Owner      string `xorm:"owner"`
	TargetKind string `xorm:"target_kind"`
	TargetName string `xorm:"target_name"`
	CreatedBy  string `xorm:"created_by"`
	State      string `xorm:"state"`
	Version    int    `xorm:"'version'"`
	CreatedAt  int64  `xorm:"created_at"`
	UpdatedAt  int64  `xorm:"updated_at"`
	DeletedAt  int64  `xorm:"deleted_at"`
}

func (shareLinkDO) TableName() string { return "storage_share_link" }
