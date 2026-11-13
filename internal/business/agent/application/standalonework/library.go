package standalonework

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// maxLibraryWorks bounds the Works one Personal Library draws from.
const maxLibraryWorks = 500

// LibraryFilter searches and narrows the Personal Library; zero fields match
// every file.
type LibraryFilter struct {
	NameQuery               string
	WorkID                  string
	AddedAfter, AddedBefore time.Time
}

// LibraryFile is one Artifact in a Personal Library.
type LibraryFile struct {
	Work query.LibraryWork
	File command.SpaceFile
}

// Library is a person's Personal Library and the Works it draws from.
type Library struct {
	Files []LibraryFile
	Works []query.LibraryWork
}

// Library lists the Artifacts of the person's Standalone Works and of each Work
// they hold a Work Share in, most recently added first. A shared Extension
// Work contributes only the Artifacts it published itself: its publication
// target may be its File Home's, holding that File Home's other Works' files.
func (p *StandaloneWorks) Library(ctx context.Context, caller Caller, filter LibraryFilter) (Library, error) {
	reader := caller.libraryReader()
	if reader == "" {
		return Library{}, access.ErrAccessDenied
	}
	agentID, _, err := p.app.FindEmployeeAgent(ctx, reader)
	if err != nil {
		return Library{}, err
	}
	works, err := p.works.ListLibraryWorks(ctx, reader, agentID, maxLibraryWorks)
	if err != nil || len(works) == 0 {
		return Library{}, err
	}
	// Only the narrowed Works' publication targets are read; every Work is
	// still listed for narrowing (Library sources).
	read := works
	if filter.WorkID != "" {
		read = slices.DeleteFunc(slices.Clone(works), func(work query.LibraryWork) bool { return work.WorkID != filter.WorkID })
	}
	var targets []domain.Directory
	for _, work := range read {
		if work.PublicationTarget.Present() && !slices.ContainsFunc(targets, work.PublicationTarget.Equal) {
			targets = append(targets, work.PublicationTarget)
		}
	}
	if len(targets) == 0 {
		return Library{Works: works}, nil
	}
	files, err := p.files.ListDirectoryFiles(ctx, targets)
	if err != nil {
		return Library{}, err
	}
	library := Library{Works: works}
	for _, file := range files {
		if file.Kind != command.FileKindArtifact {
			continue
		}
		for _, work := range read {
			if work.PublicationTarget.Equal(file.Directory) && domain.ReadableWorkFile(work.WorkID, work.Standalone, true, file.WorkID) && filter.admits(file) {
				library.Files = append(library.Files, LibraryFile{Work: work, File: file})
			}
		}
	}
	slices.SortStableFunc(library.Files, func(a, b LibraryFile) int { return b.File.UpdatedAt.Compare(a.File.UpdatedAt) })
	return library, nil
}

func (f LibraryFilter) admits(file command.SpaceFile) bool {
	if query := strings.TrimSpace(f.NameQuery); query != "" && !strings.Contains(strings.ToLower(file.Name), strings.ToLower(query)) {
		return false
	}
	if !f.AddedAfter.IsZero() && file.UpdatedAt.Before(f.AddedAfter) {
		return false
	}
	return f.AddedBefore.IsZero() || file.UpdatedAt.Before(f.AddedBefore)
}
