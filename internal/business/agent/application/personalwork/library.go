package personalwork

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/talgent-work/workbench/internal/business/agent/application/access"
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
	Work LibraryWork
	File SpaceFile
}

// Library is a person's Personal Library and the Works it draws from.
type Library struct {
	Files []LibraryFile
	Works []LibraryWork
}

// Library lists the Artifacts of the person's Personal Works and of each Work
// they hold a Work Share in, most recently added first. A shared Work with a
// Subject or a Project contributes only the Artifacts it published itself:
// its File Space may hold its caller's other Works' files.
func (p *PersonalWorks) Library(ctx context.Context, person Person, filter LibraryFilter) (Library, error) {
	reader := person.libraryReader()
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
	// Only the narrowed Works' spaces are read; every Work is still listed
	// for narrowing.
	read := works
	if filter.WorkID != "" {
		read = slices.DeleteFunc(slices.Clone(works), func(work LibraryWork) bool { return work.WorkID != filter.WorkID })
	}
	owners, err := p.spaceOwners(ctx, read)
	if err != nil {
		return Library{}, err
	}
	byOwner := make(map[string][]LibraryWork, len(read))
	for i, work := range read {
		if owners[i] != "" {
			byOwner[owners[i]] = append(byOwner[owners[i]], work)
		}
	}
	files, err := p.files.ListOwnerFiles(ctx, distinct(owners))
	if err != nil {
		return Library{}, err
	}
	library := Library{Works: works}
	for _, file := range files {
		if file.Kind != FileKindArtifact {
			continue
		}
		for _, work := range byOwner[file.Owner] {
			if domain.ReadableWorkFile(work.WorkID, work.Personal, true, file.WorkID) && filter.admits(file) {
				library.Files = append(library.Files, LibraryFile{Work: work, File: file})
			}
		}
	}
	slices.SortStableFunc(library.Files, func(a, b LibraryFile) int { return b.File.UpdatedAt.Compare(a.File.UpdatedAt) })
	return library, nil
}

// spaceOwners names each Work's File Space owner, in order: a Personal Work's
// space is named by the Work itself, other Works' only by their input
// sources.
func (p *PersonalWorks) spaceOwners(ctx context.Context, works []LibraryWork) ([]string, error) {
	owners := make([]string, len(works))
	var sources []string
	var positions []int
	for i, work := range works {
		if work.Personal {
			owners[i] = work.WorkID
			continue
		}
		sources = append(sources, work.InputSource)
		positions = append(positions, i)
	}
	if len(sources) == 0 {
		return owners, nil
	}
	resolved, err := p.files.ResolveOwners(ctx, sources)
	if err != nil {
		return nil, err
	}
	for i, owner := range resolved {
		if i < len(positions) {
			owners[positions[i]] = owner
		}
	}
	return owners, nil
}

func (f LibraryFilter) admits(file SpaceFile) bool {
	if query := strings.TrimSpace(f.NameQuery); query != "" && !strings.Contains(strings.ToLower(file.Name), strings.ToLower(query)) {
		return false
	}
	if !f.AddedAfter.IsZero() && file.UpdatedAt.Before(f.AddedAfter) {
		return false
	}
	return f.AddedBefore.IsZero() || file.UpdatedAt.Before(f.AddedBefore)
}

func distinct(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; value == "" || ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
