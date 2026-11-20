//go:build integration

package execution

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// An Extension Work on a Managed Computer mounts no Directory: its
// environment names its Work Caller and no TALGENT_KNOWLEDGE_ROOT, and it
// reaches its extension's tools through the Talgent Connector, declared at the
// machine's local proxy.
func TestAManagedExtensionWorkReadsReferencesThroughTheConnector(t *testing.T) {
	f := newManagedWork(t)
	input := managedRequest()
	input.ContextMode, input.WorkCaller = domain.ContextFresh, domain.WorkCaller{Kind: "project", ID: "project"}
	record := f.launch(t, f.begin(t, input).ID)
	require.Zero(t, f.storage.mounts, "no Directory is mounted")
	require.Equal(t, "project:project", f.relay.environment["TALGENT_WORK_CALLER"])
	require.NotContains(t, f.relay.environment, "TALGENT_KNOWLEDGE_ROOT")
	require.Contains(t, record.LaunchCommand, `"talgent":{"headers":{"Authorization":"Bearer ${TALGENT_PROXY_TOKEN}"},"type":"http","url":"${TALGENT_PROXY_URL}/mcp"}`)
}

// managedStorage stands in for Storage as a Managed Work's reconciliation
// reaches it: it counts mount acquisitions, of which a Managed start makes
// none, and opens every Directory with one Space Token. Anything else it is
// asked fails the case.
type managedStorage struct {
	command.StoragePort
	mu     sync.Mutex
	mounts int
}

func (s *managedStorage) AcquireMounts(context.Context, string, []domain.DirectoryMount) (domain.SandboxMounts, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mounts++
	return nil, nil
}

func (s *managedStorage) OpenDirectory(context.Context, domain.LocatedDirectory) (string, error) {
	return "space-token", nil
}

// caseInputs is the inputs Directory of a fresh case's File Home.
func caseInputs() domain.Directory {
	return domain.Directory{Owner: []domain.OwnerFact{{Condition: domain.ConditionFileHome, Value: "case:" + uuid.NewString()}}, Name: "inputs"}
}

// readingInputs is input reading its inputs from directory.
func readingInputs(input domain.LaunchSpecification, directory domain.Directory) domain.LaunchSpecification {
	located := domain.LocatedDirectory{Directory: directory, Backend: domain.BackendJuiceFS, Lifetime: domain.LifetimePermanent}
	input.LocatedLayout.Inputs = &located
	return input
}
