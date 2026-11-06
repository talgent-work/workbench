package infrastructure

type catalogReleaseDO struct {
	ID       string `xorm:"id pk"`
	Metadata string `xorm:"metadata"`
}

func (catalogReleaseDO) TableName() string { return "marketplace_plugin_release" }

type catalogEntryDO struct {
	ID         string `xorm:"id pk"`
	Generation string `xorm:"generation"`
	Source     string `xorm:"source"`
	Slug       string `xorm:"slug"`
	ReleaseID  string `xorm:"release_id"`
	Selected   bool   `xorm:"selected"`
	HasSkill   bool   `xorm:"has_skill"`
	HasAgent   bool   `xorm:"has_agent"`
	HasMCP     bool   `xorm:"has_mcp"`
	Version    int    `xorm:"version"`
	CreatedAt  int64  `xorm:"created_at"`
	UpdatedAt  int64  `xorm:"updated_at"`
	DeletedAt  int64  `xorm:"deleted_at"`
}

func (catalogEntryDO) TableName() string { return "marketplace_catalog_entry" }
