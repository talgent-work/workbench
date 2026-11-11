package execution

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

func twoPluginSnapshot() domain.CapabilitySnapshot {
	return domain.CapabilitySnapshot{Plugins: []domain.SelectedPlugin{{Release: domain.PluginRelease{Slug: "mattpocock-skills"}}, {Release: domain.PluginRelease{Slug: "talgent"}}}}
}

// execd re-runs a finished preparation when it is attached again, so the
// replayed output holds one report per run (OP-7 held three).
func TestRepeatedPreparationReportsKeepEachPluginsOutcome(t *testing.T) {
	report := `{"results":[{"slug":"mattpocock-skills","installed":false,"reason":"installation_failed"},{"slug":"talgent","installed":true,"reason":""}]}` + "\n"
	results := installationResults([]byte(report+report+report), 0, twoPluginSnapshot())
	require.Equal(t, []domain.PluginInstallation{
		{Slug: "mattpocock-skills", Installed: false, Reason: "installation_failed"},
		{Slug: "talgent", Installed: true, Reason: ""},
	}, results)
}

func TestTheLatestCompletePreparationReportWins(t *testing.T) {
	first := `{"results":[{"slug":"mattpocock-skills","installed":false,"reason":"installation_failed"},{"slug":"talgent","installed":true,"reason":""}]}` + "\n"
	latest := `{"results":[{"slug":"mattpocock-skills","installed":true,"reason":""},{"slug":"talgent","installed":true,"reason":""}]}` + "\n"
	results := installationResults([]byte(first+latest), 0, twoPluginSnapshot())
	require.True(t, results[0].Installed, "a later run's report describes the current installation")
}

func TestUnusablePreparationOutputStillFailsEveryPlugin(t *testing.T) {
	for _, output := range []string{"", "not json\n", `{"results":[{"slug":"talgent","installed":true,"reason":""}]}` + "\n"} {
		results := installationResults([]byte(output), 0, twoPluginSnapshot())
		for _, result := range results {
			require.Equal(t, "preparation_failed", result.Reason, output)
		}
	}
	valid := `{"results":[{"slug":"mattpocock-skills","installed":true,"reason":""},{"slug":"talgent","installed":true,"reason":""}]}` + "\n"
	for _, result := range installationResults([]byte(valid), 1, twoPluginSnapshot()) {
		require.Equal(t, "preparation_failed", result.Reason, "a failed exit is not trusted")
	}
}
