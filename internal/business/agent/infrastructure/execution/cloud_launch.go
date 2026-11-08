package execution

import "github.com/talgent-work/workbench/internal/business/agent/domain"

// CloudLaunch is the Harness launch a Cloud sandbox runs for a start.
type CloudLaunch struct {
	// Command is the shell command that runs the Harness.
	Command string
	// SessionID is the Harness conversation's id.
	SessionID string
	// Diagnostic is the file the Harness's diagnostics go to.
	Diagnostic string
}

// CloudLaunch renders the Harness launch of request for owner as a Cloud
// sandbox runs it, for a caller that runs the Harness outside one: the
// integration tests of a module that cannot reach this package's internals
// (internal/testsupport/agenttest). Production starts go through Sandboxes.
func (c NativeLaunchConfig) CloudLaunch(request domain.LaunchSpecification, owner SandboxOwner) (CloudLaunch, error) {
	launch, err := c.prepare(sandboxLayout, request, owner, "")
	if err != nil {
		return CloudLaunch{}, err
	}
	return CloudLaunch{Command: launch.Command, SessionID: launch.SessionID, Diagnostic: sandboxDiagnostics + diagnosticName(owner)}, nil
}
