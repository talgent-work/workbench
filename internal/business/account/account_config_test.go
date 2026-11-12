package account

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func validConfig() Config {
	return Config{
		AccessTokenLifetime:      "15m",
		AgentTokenLifetime:       "168h",
		ApplicationTokenLifetime: "15m",
		ApplicationSecretOverlap: "24h",
		SessionSlidingLifetime:   "720h",
		SessionAbsoluteLifetime:  "2160h",
		KeyRotationInterval:      "720h",
		KeyOverlap:               "24h",
		MasterKeyPath:            "/run/secrets/account-master-key",
	}
}

func TestConfigKeepsAnApplicationsTokenShort(t *testing.T) {
	cfg := validConfig()
	require.NoError(t, cfg.Validate())

	cfg.ApplicationTokenLifetime = "10m"
	require.NoError(t, cfg.Validate())

	cfg.ApplicationTokenLifetime = "16m"
	require.ErrorContains(t, cfg.Validate(), "application-token-lifetime must not be longer than access-token-lifetime")

	cfg.ApplicationTokenLifetime = "168h"
	require.Error(t, cfg.Validate())
}
