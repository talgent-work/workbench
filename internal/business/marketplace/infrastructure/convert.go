package infrastructure

import (
	"encoding/json"
	"errors"

	"github.com/talgent-work/workbench/internal/business/marketplace/application/query"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

func installationToDO(plugin *domain.InstalledPlugin, encrypted string) installedPluginDO {
	return installedPluginDO{ID: plugin.ID, OwnerID: plugin.OwnerID, Slug: plugin.Slug, State: uint8(plugin.State), Configuration: encrypted, Version: plugin.Version}
}

func installationFromDO(row installedPluginDO, manual domain.Configuration) (*domain.InstalledPlugin, error) {
	plugin := &domain.InstalledPlugin{ID: row.ID, OwnerID: row.OwnerID, Slug: row.Slug, State: domain.InstallationState(row.State), ManualConfiguration: manual.Clone(), Version: row.Version}
	if row.Version < 1 {
		return nil, domain.ErrInvalidInstallation
	}
	if err := plugin.Validate(); err != nil {
		return nil, err
	}
	return plugin, nil
}

func installationReadModel(row installedPluginDO, manual domain.Configuration) query.InstalledPlugin {
	return query.InstalledPlugin{OwnerID: row.OwnerID, Slug: row.Slug, Enabled: row.State == uint8(domain.Enabled), Configuration: manual.Clone(), Version: row.Version}
}

func decodeConfiguration(encoded []byte) (domain.Configuration, error) {
	var values map[string]any
	if err := json.Unmarshal(encoded, &values); err != nil {
		return nil, errors.New("marketplace: corrupt stored configuration")
	}
	result := make(domain.Configuration, len(values))
	for name, value := range values {
		switch typed := value.(type) {
		case string, float64, bool:
			result[name] = typed
		case []any:
			items := make([]string, len(typed))
			for index, item := range typed {
				text, ok := item.(string)
				if !ok {
					return nil, errors.New("marketplace: corrupt stored configuration")
				}
				items[index] = text
			}
			result[name] = items
		default:
			return nil, errors.New("marketplace: corrupt stored configuration")
		}
	}
	return result, nil
}
