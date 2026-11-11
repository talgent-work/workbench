package infrastructure

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/samber/oops"
	"golang.org/x/sys/unix"

	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

// The Mapped View lies beside the Directories, under the layout's
// MappedView: a mapping at path P is the folder MappedView/P holding a hard
// link to each file of its Directory, at the file's name or, for a
// Directory whose content is replaced as a whole, at its path in the current
// content. A hard link is the same file, so its content and attributes come
// along and nothing is copied; the Mapped View and its Directories share one
// file system. Only Storage writes it, holding the mapped Directory's lock.

// errNoMappedView means the file system keeps no Mapped View.
var errNoMappedView = errors.New("storage: this backend keeps no Mapped View")

// Lock takes the Directory's lock exclusively.
func (f *DirectoryFiles) Lock(ctx context.Context, id domain.DirectoryID) (func(), error) {
	return f.lock(ctx, id, unix.LOCK_EX)
}

// view is a mapped path's folder on the file system.
func (f *DirectoryFiles) view(mapped domain.MappedPath) (string, error) {
	if f.layout.MappedView == "" {
		return "", errNoMappedView
	}
	return filepath.Join(f.layout.Root, filepath.FromSlash(f.layout.MappedView), filepath.FromSlash(string(mapped))), nil
}

// MapFiles links every file of the Directory missing at the mapped path and
// removes whatever else is there: a file the Directory no longer holds, one
// it replaced since, and the folders that leaves empty. The mapping's own
// folder always exists, so a Directory without files has its place.
func (f *DirectoryFiles) MapFiles(ctx context.Context, id domain.DirectoryID, mapped domain.MappedPath) error {
	operation := oops.With("operation", "storage.map_files", "directory", f.relative(id), "mapped_path", string(mapped))
	view, err := f.view(mapped)
	if err != nil {
		return operation.Wrap(err)
	}
	sources, err := f.mappedFiles(id)
	if err != nil {
		return operation.Wrap(err)
	}
	if err := os.MkdirAll(view, directoryMode); err != nil {
		return operation.Wrap(err)
	}
	if err := pruneView(view, sources); err != nil {
		return operation.Wrap(err)
	}
	for _, relative := range sortedKeys(sources) {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := linkInto(view, relative, sources[relative]); err != nil {
			return operation.Wrap(err)
		}
	}
	return nil
}

// UnmapFiles removes the mapped path's folder and each folder above it that
// it leaves empty.
func (f *DirectoryFiles) UnmapFiles(ctx context.Context, mapped domain.MappedPath) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	operation := oops.With("operation", "storage.unmap_files", "mapped_path", string(mapped))
	view, err := f.view(mapped)
	if err != nil {
		return operation.Wrap(err)
	}
	if err := os.RemoveAll(view); err != nil {
		return operation.Wrap(err)
	}
	return operation.Wrap(removeEmptyParents(filepath.Join(f.layout.Root, filepath.FromSlash(f.layout.MappedView)), view))
}

// mappedFiles are the Directory's files by the relative path a mapping
// shows each at, with where each lies: the regular files directly in its
// folder, or, once its content was replaced as a whole, the current
// content's files outside its hidden top-level entries.
func (f *DirectoryFiles) mappedFiles(id domain.DirectoryID) (map[string]string, error) {
	folder := f.folder(id)
	content, err := f.current(id)
	if err != nil {
		return nil, err
	}
	files := map[string]string{}
	if content != "" {
		root := filepath.Join(folder, contentsFolder, content)
		relatives, err := filesUnder(root)
		if err != nil {
			return nil, err
		}
		for _, relative := range relatives {
			if !strings.HasPrefix(string(relative), ".") {
				files[string(relative)] = filepath.Join(root, filepath.FromSlash(string(relative)))
			}
		}
		return files, nil
	}
	entries, err := os.ReadDir(folder)
	if errors.Is(err, fs.ErrNotExist) {
		return files, nil
	}
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.Type().IsRegular() && domain.ValidateFileName(entry.Name()) == nil {
			files[entry.Name()] = filepath.Join(folder, entry.Name())
		}
	}
	return files, nil
}

// pruneView removes every entry of a mapping's folder that is not a link to
// its source file, and every folder that leaves empty below it.
func pruneView(view string, sources map[string]string) error {
	var folders []string
	err := filepath.WalkDir(view, func(name string, entry fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(view, name)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if relative != "." {
				folders = append(folders, name)
			}
			return nil
		}
		source, wanted := sources[filepath.ToSlash(relative)]
		if wanted && entry.Type().IsRegular() && sameFile(name, source) {
			return nil
		}
		return removeEntry(name)
	})
	if err != nil {
		return err
	}
	// Deepest first, so a folder emptied by its children goes too.
	slices.Reverse(folders)
	for _, folder := range folders {
		if err := unix.Rmdir(folder); err != nil && !errors.Is(err, unix.ENOTEMPTY) && !errors.Is(err, unix.EEXIST) && !errors.Is(err, unix.ENOENT) {
			return err
		}
	}
	return nil
}

// linkInto links source at relative inside a mapping's folder unless that
// file is already there; a source removed meanwhile is not linked.
func linkInto(view, relative, source string) error {
	target := filepath.Join(view, filepath.FromSlash(relative))
	if sameFile(target, source) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(target), directoryMode); err != nil {
		return err
	}
	if err := removeEntry(target); err != nil {
		return err
	}
	// Linkat without AT_SYMLINK_FOLLOW links the entry itself, never what a
	// link points at.
	err := unix.Linkat(unix.AT_FDCWD, source, unix.AT_FDCWD, target, 0)
	if errors.Is(err, unix.ENOENT) && !exists(source) {
		return nil
	}
	return err
}

func removeEntry(name string) error {
	if err := os.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// removeEmptyParents removes each folder above name up to root while it is
// empty.
func removeEmptyParents(root, name string) error {
	for parent := filepath.Dir(name); parent != root && strings.HasPrefix(parent, root+string(filepath.Separator)); parent = filepath.Dir(parent) {
		err := unix.Rmdir(parent)
		if errors.Is(err, unix.ENOTEMPTY) || errors.Is(err, unix.EEXIST) {
			return nil
		}
		if err != nil && !errors.Is(err, unix.ENOENT) {
			return err
		}
	}
	return nil
}

// sameFile reports whether both names are one inode, neither a link.
func sameFile(a, b string) bool {
	var first, second unix.Stat_t
	if unix.Lstat(a, &first) != nil || unix.Lstat(b, &second) != nil {
		return false
	}
	return first.Dev == second.Dev && first.Ino == second.Ino && first.Mode&unix.S_IFMT == unix.S_IFREG
}

func exists(name string) bool {
	_, err := os.Lstat(name)
	return err == nil
}

func sortedKeys(files map[string]string) []string {
	keys := make([]string, 0, len(files))
	for key := range files {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}
