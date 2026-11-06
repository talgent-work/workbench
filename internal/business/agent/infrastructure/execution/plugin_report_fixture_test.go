package execution

import "github.com/talgent-work/workbench/internal/business/agent/domain"

// installationResults reads the Plugin outcomes of the latest complete report
// of a start that checks nothing out.
func installationResults(output []byte, exit int, snapshot domain.CapabilitySnapshot) []pluginInstallationResult {
	return latestReport(output, exit, snapshot, nil).plugins(snapshot)
}
