package infrastructure

import (
	"encoding/json"
	"errors"

	"github.com/talgent-work/workbench/internal/business/marketplace/application"
)

func decodeRelease(encoded string) (application.PluginRelease, error) {
	var release application.PluginRelease
	if err := json.Unmarshal([]byte(encoded), &release); err != nil {
		return release, errors.New("marketplace: corrupt release metadata")
	}
	return release, nil
}
