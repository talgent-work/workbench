package infrastructure

type connectorDO struct {
	ID             string `xorm:"id pk"`
	Name           string `xorm:"name"`
	State          uint8  `xorm:"state"`
	Required       bool   `xorm:"required"`
	Description    string `xorm:"description"`
	Category       string `xorm:"category"`
	Configuration  string `xorm:"configuration"`
	Authentication uint8  `xorm:"authentication"`
	Version        int    `xorm:"version"`
	CreatedAt      int64  `xorm:"created_at"`
	UpdatedAt      int64  `xorm:"updated_at"`
	DeletedAt      int64  `xorm:"deleted_at"`
}

func (connectorDO) TableName() string { return "marketplace_connector" }

var connectorColumns = []string{"id", "name", "state", "required", "description", "category", "configuration", "authentication", "version"}

type connectorConfigurationRecord struct {
	Type        string                `json:"type,omitempty"`
	URL         string                `json:"url,omitempty"`
	Environment map[string]string     `json:"env,omitempty"`
	Headers     map[string]string     `json:"headers,omitempty"`
	Provider    *providerAccessRecord `json:"provider,omitempty"`
	Command     string                `json:"command,omitempty"`
	Arguments   []string              `json:"args,omitempty"`
}

type providerAccessRecord struct {
	Provider       string `json:"provider"`
	Target         string `json:"target"`
	Authentication string `json:"authentication"`
}

type installedConnectorDO struct {
	ID          string `xorm:"id pk"`
	UserID      string `xorm:"user_id"`
	ConnectorID string `xorm:"connector_id"`
	State       uint8  `xorm:"state"`
	Version     int    `xorm:"version"`
	CreatedAt   int64  `xorm:"created_at"`
	UpdatedAt   int64  `xorm:"updated_at"`
	DeletedAt   int64  `xorm:"deleted_at"`
}

func (installedConnectorDO) TableName() string { return "marketplace_installed_connector" }

var installedConnectorColumns = []string{"id", "user_id", "connector_id", "state", "version"}
