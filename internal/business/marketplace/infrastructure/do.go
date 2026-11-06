package infrastructure

type installedPluginDO struct {
	ID            string `xorm:"id pk"`
	UserID        string `xorm:"user_id"`
	Slug          string `xorm:"slug"`
	State         uint8  `xorm:"state"`
	Configuration string `xorm:"configuration"`
	Version       int    `xorm:"version"`
	CreatedAt     int64  `xorm:"created_at"`
	UpdatedAt     int64  `xorm:"updated_at"`
	DeletedAt     int64  `xorm:"deleted_at"`
}

func (installedPluginDO) TableName() string { return "marketplace_installed_plugin" }

var installationColumns = []string{"id", "user_id", "slug", "state", "configuration", "version", "created_at", "updated_at", "deleted_at"}
