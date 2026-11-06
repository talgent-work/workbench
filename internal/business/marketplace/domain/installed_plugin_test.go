package domain_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

func parameters() domain.ParameterDeclarations {
	return domain.ParameterDeclarations{
		"language": {Type: "string", Title: "Language", Description: "Output language", Default: "zh"},
		"enabled":  {Type: "boolean", Title: "Enabled", Description: "Enable feature", Default: true},
		"count":    {Type: "number", Title: "Count", Description: "Result count", Default: float64(3)},
		"paths":    {Type: "string", Title: "Paths", Description: "Included paths", Multiple: true},
	}
}

func TestPersonalInstallationKeepsOnlyManualValues(t *testing.T) {
	declarations := parameters()
	plugin, err := domain.Install("user-a", "reviewer", nil, declarations)
	require.NoError(t, err)
	require.Empty(t, plugin.ManualConfiguration, "reading defaults must not create manual choices")
	effective, err := plugin.ManualConfiguration.Effective(declarations)
	require.NoError(t, err)
	require.Equal(t, "zh", effective["language"])
	require.NoError(t, plugin.SetConfiguration(domain.Configuration{"language": "zh", "enabled": false, "count": float64(0)}, declarations))
	require.Len(t, plugin.ManualConfiguration, 3)
	effective, err = plugin.ManualConfiguration.Effective(declarations)
	require.NoError(t, err)
	require.Equal(t, false, effective["enabled"])
	require.Equal(t, float64(0), effective["count"])
	plugin.Disable()
	require.Equal(t, domain.Disabled, plugin.State)
	require.Len(t, plugin.ManualConfiguration, 3)
	plugin.Enable()
	require.Equal(t, domain.Enabled, plugin.State)
	require.NoError(t, plugin.DeleteConfiguration([]string{"language"}, declarations))
	require.NotContains(t, plugin.ManualConfiguration, "language")
	plugin.Uninstall()
	require.Empty(t, plugin.ManualConfiguration)
}

func TestInvalidManualValueNeverFallsBackOrPartiallyChangesInstallation(t *testing.T) {
	declarations := parameters()
	plugin, err := domain.Install("user-a", "reviewer", domain.Configuration{"language": "en"}, declarations)
	require.NoError(t, err)
	plugin.Disable()
	for _, invalid := range []any{nil, true, float64(5), []string{"en"}} {
		err = plugin.SetConfiguration(domain.Configuration{"language": invalid, "count": float64(9)}, declarations)
		require.ErrorIs(t, err, domain.ErrInvalidConfiguration)
		require.Equal(t, domain.Configuration{"language": "en"}, plugin.ManualConfiguration)
		require.Equal(t, domain.Disabled, plugin.State)
	}
	_, err = domain.Configuration{"enabled": "false"}.Effective(declarations)
	require.ErrorIs(t, err, domain.ErrInvalidConfiguration, "a string is not a boolean")
}

func TestDeclarationEvolutionDoesNotRewritePersonalConfiguration(t *testing.T) {
	plugin, err := domain.Install("user-a", "reviewer", domain.Configuration{"language": "en"}, parameters())
	require.NoError(t, err)
	changed := domain.ParameterDeclarations{
		"endpoint": {Type: "string", Title: "Endpoint", Description: "Service endpoint", Required: true},
	}
	_, err = plugin.ManualConfiguration.Effective(changed)
	require.ErrorIs(t, err, domain.ErrInvalidConfiguration)
	require.NoError(t, plugin.SetConfiguration(domain.Configuration{"endpoint": "https://example.com"}, changed))
	effective, err := plugin.ManualConfiguration.Effective(changed)
	require.NoError(t, err)
	require.NotContains(t, effective, "language", "removed parameters are not passed to the runtime")
	require.Equal(t, "en", plugin.ManualConfiguration["language"], "catalog changes never erase a manual choice")
	err = plugin.DeleteConfiguration([]string{"endpoint"}, changed)
	require.ErrorIs(t, err, domain.ErrInvalidConfiguration)
	require.Contains(t, plugin.ManualConfiguration, "endpoint", "rejected deletion leaves the configuration intact")
}

func TestConfigurationCopiesMultipleValuesAtEveryBoundary(t *testing.T) {
	paths := []string{"src"}
	plugin, err := domain.Install("user-a", "reviewer", domain.Configuration{"paths": paths}, parameters())
	require.NoError(t, err)
	paths[0] = "outside"
	effective, err := plugin.ManualConfiguration.Effective(parameters())
	require.NoError(t, err)
	require.Equal(t, []string{"src"}, effective["paths"])
	effective["paths"].([]string)[0] = "mutated"
	require.Equal(t, []string{"src"}, plugin.ManualConfiguration["paths"])
}

func TestNumberBoundsAndSensitiveErrors(t *testing.T) {
	min, max := float64(1), float64(5)
	declarations := domain.ParameterDeclarations{
		"token": {Type: "string", Title: "Token", Description: "Access token", Sensitive: true, Required: true},
		"count": {Type: "number", Title: "Count", Description: "Result count", Min: &min, Max: &max},
	}
	for _, number := range []float64{0, 6} {
		_, err := domain.Install("user-a", "reviewer", domain.Configuration{"token": "secret-value", "count": number}, declarations)
		require.ErrorIs(t, err, domain.ErrInvalidConfiguration)
		require.NotContains(t, err.Error(), "secret-value")
	}
	_, err := domain.Install("user-a", "reviewer", domain.Configuration{"token": "secret-value", "count": float64(5)}, declarations)
	require.NoError(t, err)
}
