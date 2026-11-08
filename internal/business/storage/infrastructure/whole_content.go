package infrastructure

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/samber/oops"
	"golang.org/x/sys/unix"

	"github.com/talgent-work/workbench/internal/business/storage/application"
	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

// A Directory whose content is replaced as a whole: its current content is
// the relative symbolic link ".current" to one directory under ".contents",
// and every visible entry of that content appears in the Directory as a
// relative link through ".current". A mount resolves the links afresh on
// every access, so the Directory reads as the current content and follows
// each replacement at once. A new content is staged outside the Directory, so
// content never switched in is never visible, and content switched out is
// moved out of it at once and removed before the replacement completes. A
// replacement holds the Directory's lock exclusively and a read through
// Storage holds it shared, across every controlplane replica, so
// replacements never interleave and such a read sees one whole content.
const (
	currentContent = ".current"
	contentsFolder = ".contents"
)

func (f *DirectoryFiles) folder(id domain.DirectoryID) string {
	return filepath.Join(f.layout.Root, filepath.FromSlash(f.relative(id)))
}

// current is the current content's directory name, empty before the first
// replacement.
func (f *DirectoryFiles) current(id domain.DirectoryID) (string, error) {
	target, err := os.Readlink(filepath.Join(f.folder(id), currentContent))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return path.Base(target), nil
}

func (f *DirectoryFiles) ReadContent(ctx context.Context, id domain.DirectoryID, paths []domain.ContentPath) ([]application.ContentFile, error) {
	unlock, err := f.lock(ctx, id, unix.LOCK_SH)
	if err != nil {
		return nil, err
	}
	defer unlock()
	content, err := f.current(id)
	if err != nil || content == "" {
		return nil, err
	}
	directory := filepath.Join(f.folder(id), contentsFolder, content)
	if len(paths) == 0 {
		paths, err = filesUnder(directory)
		if err != nil {
			return nil, oops.With("operation", "storage.read_content", "directory", f.relative(id)).Wrap(err)
		}
	}
	var files []application.ContentFile
	for _, file := range paths {
		data, err := os.ReadFile(filepath.Join(directory, filepath.FromSlash(string(file))))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, oops.With("operation", "storage.read_content", "directory", f.relative(id)).Wrap(err)
		}
		files = append(files, application.ContentFile{Path: file, Content: data})
	}
	return files, nil
}

// filesUnder lists the regular files of a content by their relative paths.
func filesUnder(directory string) ([]domain.ContentPath, error) {
	var out []domain.ContentPath
	err := filepath.WalkDir(directory, func(name string, entry fs.DirEntry, err error) error {
		if err != nil || !entry.Type().IsRegular() {
			return err
		}
		relative, err := filepath.Rel(directory, name)
		if err != nil {
			return err
		}
		out = append(out, domain.ContentPath(filepath.ToSlash(relative)))
		return nil
	})
	return out, err
}

// ReplaceContent stages the files aside, moves them into the Directory,
// switches it to them in one rename and removes every earlier content,
// holding the Directory's lock so its replacements run one at a time.
func (f *DirectoryFiles) ReplaceContent(ctx context.Context, id domain.DirectoryID, files []application.ContentFile) error {
	unlock, err := f.lock(ctx, id, unix.LOCK_EX)
	if err != nil {
		return err
	}
	defer unlock()
	operation := oops.With("operation", "storage.replace_content", "directory", f.relative(id))
	space := f.folder(id)
	if _, err := os.Lstat(space); err != nil {
		return operation.Wrap(err)
	}
	staging := f.areaPath(stagingArea, id)
	// Any earlier staging was abandoned before it switched in.
	if err := os.RemoveAll(staging); err != nil {
		return operation.Wrap(err)
	}
	content := uuid.Must(uuid.NewV7()).String()
	staged := filepath.Join(staging, content)
	if err := os.MkdirAll(staged, directoryMode); err != nil {
		return operation.Wrap(err)
	}
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := writeContentFile(staged, file); err != nil {
			return operation.Wrap(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(space, contentsFolder), directoryMode); err != nil {
		return operation.Wrap(err)
	}
	switched := filepath.Join(space, contentsFolder, content)
	if err := os.Rename(staged, switched); err != nil {
		return operation.Wrap(err)
	}
	// The link is made aside and renamed over ".current", so a reader finds
	// either the previous content or this one, never neither.
	if err := replaceLink(space, currentContent, path.Join(contentsFolder, content)); err != nil {
		return operation.Wrap(err)
	}
	if err := linkEntries(space, switched); err != nil {
		return operation.Wrap(err)
	}
	if err := syncDirectory(space); err != nil {
		return operation.Wrap(err)
	}
	return f.removeEarlier(id, content)
}

func writeContentFile(content string, file application.ContentFile) error {
	name := filepath.Join(content, filepath.FromSlash(string(file.Path)))
	if err := os.MkdirAll(filepath.Dir(name), directoryMode); err != nil {
		return err
	}
	return os.WriteFile(name, file.Content, fileMode)
}

// removeEarlier moves every content but the current one out of the
// Directory, beyond the reach of a mount, then removes it; a replacement
// completes only once no earlier content stays readable.
func (f *DirectoryFiles) removeEarlier(id domain.DirectoryID, current string) error {
	operation := oops.With("operation", "storage.remove_earlier_content", "directory", f.relative(id))
	contents := filepath.Join(f.folder(id), contentsFolder)
	entries, err := os.ReadDir(contents)
	if err != nil {
		return operation.Wrap(err)
	}
	retired := f.areaPath(retiredArea, id)
	for _, entry := range entries {
		if entry.Name() == current {
			continue
		}
		if err := os.MkdirAll(retired, directoryMode); err != nil {
			return operation.Wrap(err)
		}
		// Each move takes a fresh name, so one left by an earlier attempt is
		// never in the way.
		target := filepath.Join(retired, entry.Name()+"-"+uuid.Must(uuid.NewV7()).String())
		if err := os.Rename(filepath.Join(contents, entry.Name()), target); err != nil {
			return operation.Wrap(err)
		}
	}
	if err := os.RemoveAll(retired); err != nil {
		return operation.Wrap(err)
	}
	// A link left by a switch that failed before its rename is no entry.
	links, err := filepath.Glob(filepath.Join(f.folder(id), ".link-*"))
	failures := []error{err}
	for _, link := range links {
		if err := os.Remove(link); err != nil && !errors.Is(err, fs.ErrNotExist) {
			failures = append(failures, err)
		}
	}
	return operation.Wrap(errors.Join(failures...))
}

// linkEntries makes the Directory's visible entries the content's: each
// links through ".current", so it already shows the switched content; only
// an entry the content adds or drops is linked or unlinked here.
func linkEntries(space, content string) error {
	entries, err := os.ReadDir(content)
	if err != nil {
		return err
	}
	wanted := map[string]bool{}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		wanted[entry.Name()] = true
		target := path.Join(currentContent, entry.Name())
		if existing, err := os.Readlink(filepath.Join(space, entry.Name())); err == nil && existing == target {
			continue
		}
		if err := replaceLink(space, entry.Name(), target); err != nil {
			return err
		}
	}
	present, err := os.ReadDir(space)
	if err != nil {
		return err
	}
	for _, entry := range present {
		if strings.HasPrefix(entry.Name(), ".") || wanted[entry.Name()] || entry.Type()&fs.ModeSymlink == 0 {
			continue
		}
		if err := os.Remove(filepath.Join(space, entry.Name())); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

// replaceLink points the Directory's entry at target in one rename.
func replaceLink(space, name, target string) error {
	link := filepath.Join(space, ".link-"+uuid.Must(uuid.NewV7()).String())
	if err := os.Symlink(target, link); err != nil {
		return err
	}
	if err := os.Rename(link, filepath.Join(space, name)); err != nil {
		return errors.Join(err, os.Remove(link))
	}
	return nil
}

func syncDirectory(name string) error {
	directory, err := os.Open(name)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
