//go:build integration

package execution

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// A Work of a Project on a Managed Computer has no read-only mount, even
// one its creating caller supplied: its environment names its Project and no
// TALGENT_KNOWLEDGE_ROOT, and it reaches its Project's tools through the
// Talgent Connector, declared at the machine's local proxy.
func TestAManagedWorkOfAProjectReadsReferencesThroughTheConnector(t *testing.T) {
	f := newManagedWork(t)
	input := managedRequest()
	input.ContextMode, input.ProjectID = domain.ContextFresh, "project"
	input.ReadOnlyMounts = []domain.ReadOnlyMount{{Name: "reference", Reference: "project"}}
	record := f.launch(t, f.begin(t, input).ID)
	require.Zero(t, f.mounts.calls, "no read-only mount is acquired")
	require.Equal(t, "project", f.relay.environment["TALGENT_PROJECT_ID"])
	require.NotContains(t, f.relay.environment, "TALGENT_KNOWLEDGE_ROOT")
	require.Contains(t, record.LaunchCommand, `"talgent":{"headers":{"Authorization":"Bearer ${TALGENT_PROXY_TOKEN}"},"type":"http","url":"${TALGENT_PROXY_URL}/mcp"}`)
}

// countingMounts counts mount acquisitions; a Managed start makes none.
type countingMounts struct {
	command.StoragePort
	calls int
}

func (m *countingMounts) AcquireMounts(context.Context, domain.SandboxMountRequest) (domain.SandboxMounts, error) {
	m.calls++
	return domain.SandboxMounts{}, nil
}
