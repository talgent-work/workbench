package commandtest

import (
	"context"
	"sync"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// Storage stands in for Storage's contract as an execution reaches it: it
// grants one Mount Grant per requested Directory and records each request,
// opens every Directory with the same Space Token, lists the files a test
// placed in a Directory, and records reclamation requests.
type Storage struct {
	command.StoragePort
	mu sync.Mutex
	// Files are the files of each Directory, by its name.
	Files map[string][]command.SpaceFile
	// Acquired are the mounts each start requested, in order.
	Acquired [][]domain.DirectoryMount
	// Reclaimed are the Works whose reclamation was requested.
	Reclaimed []string
}

// SpaceToken is the token every Directory opens with here.
const SpaceToken = "space-token"

func (s *Storage) AcquireMounts(_ context.Context, _ string, mounts []domain.DirectoryMount) (domain.SandboxMounts, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Acquired = append(s.Acquired, mounts)
	grants := make(domain.SandboxMounts, 0, len(mounts))
	for _, mount := range mounts {
		grants = append(grants, domain.MountGrant{ClaimName: "claim", SubPath: mount.Directory.Directory.Name, ReadOnly: mount.ReadOnly})
	}
	return grants, nil
}

func (s *Storage) OpenDirectory(context.Context, domain.LocatedDirectory) (string, error) {
	return SpaceToken, nil
}

func (s *Storage) ListFiles(_ context.Context, directory domain.Directory) ([]command.SpaceFile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Files[directory.Name], nil
}

func (s *Storage) IssueAccessLink(_ context.Context, position command.FilePosition, _ bool) (command.FileLink, error) {
	return command.FileLink{Path: "/files/" + position.Directory.Name + "/" + position.Name + "?grant=1"}, nil
}

func (s *Storage) RequestWorkReclamation(_ context.Context, workID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Reclaimed = append(s.Reclaimed, workID)
	return nil
}
