//go:build integration

package execution

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
)

// A Work of a Project on a Managed Computer mounts no Directory: its
// environment names its Project and no TALGENT_KNOWLEDGE_ROOT, and it reaches
// its Project's tools through the Talgent Connector, declared at the
// machine's local proxy.
func TestAManagedWorkOfAProjectReadsReferencesThroughTheConnector(t *testing.T) {
	f := newManagedWork(t)
	input := managedRequest()
	input.ContextMode, input.ProjectID = domain.ContextFresh, "project"
	record := f.launch(t, f.begin(t, input).ID)
	require.Zero(t, f.storage.mounts, "no Directory is mounted")
	require.Equal(t, "project", f.relay.environment["TALGENT_PROJECT_ID"])
	require.NotContains(t, f.relay.environment, "TALGENT_KNOWLEDGE_ROOT")
	require.Contains(t, record.LaunchCommand, `"talgent":{"headers":{"Authorization":"Bearer ${TALGENT_PROXY_TOKEN}"},"type":"http","url":"${TALGENT_PROXY_URL}/mcp"}`)
}

// managedStorage stands in for Storage as a Managed Work's reconciliation
// reaches it: it counts mount acquisitions, of which a Managed start makes
// none, opens every Directory with one Space Token, and lists the files of
// each Directory with their Access Links.
type managedStorage struct {
	command.StoragePort
	mu     sync.Mutex
	mounts int
	// files are each Directory's files, by its key.
	files map[string][]managedFile
	err   error
	// bound is how long the last listing had left when it began, zero when
	// it was unbounded.
	bound time.Duration
}

// managedFile is a file of a Directory and the path of its Access Link.
type managedFile struct {
	command.SpaceFile
	link string
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

func (s *managedStorage) ListFiles(ctx context.Context, directory domain.Directory) ([]command.SpaceFile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bound = 0
	if deadline, ok := ctx.Deadline(); ok {
		s.bound = time.Until(deadline)
	}
	if s.err != nil {
		return nil, s.err
	}
	files := []command.SpaceFile{}
	for _, file := range s.files[infrastructure.DirectoryKey(directory)] {
		files = append(files, file.SpaceFile)
	}
	return files, nil
}

func (s *managedStorage) IssueAccessLink(_ context.Context, position command.FilePosition, _ bool) (command.FileLink, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, file := range s.files[infrastructure.DirectoryKey(position.Directory)] {
		if file.Name == position.Name {
			return command.FileLink{Path: file.link}, nil
		}
	}
	return command.FileLink{}, command.ErrFileNotFound
}

// place puts files in directory.
func (s *managedStorage) place(directory domain.Directory, files ...managedFile) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.files == nil {
		s.files = map[string][]managedFile{}
	}
	s.files[infrastructure.DirectoryKey(directory)] = files
}

// caseInputs is the inputs Directory of a fresh case.
func caseInputs() domain.Directory {
	return domain.Directory{Owner: []domain.OwnerFact{{Condition: domain.ConditionSubject, Value: "case:" + uuid.NewString()}}, Name: "inputs"}
}

// readingInputs is input reading its inputs from directory.
func readingInputs(input domain.LaunchSpecification, directory domain.Directory) domain.LaunchSpecification {
	located := domain.LocatedDirectory{Directory: directory, Backend: domain.BackendJuiceFS, Lifetime: domain.LifetimePermanent}
	input.LocatedLayout.Inputs = &located
	return input
}
