package infrastructure

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/samber/oops"
	"golang.org/x/sys/unix"

	"github.com/talgent-work/workbench/internal/business/storage/application"
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

// mappedViewLock names the Mapped View's lock among the Directories' locks;
// no Directory key, a hex digest, has its form.
const mappedViewLock = "mapped-view"

// LockMappedView takes the Mapped View's lock exclusively.
func (f *DirectoryFiles) LockMappedView(ctx context.Context) (func(), error) {
	if f.layout.MappedView == "" {
		return nil, errNoMappedView
	}
	return f.lockNamed(ctx, mappedViewLock, unix.LOCK_EX)
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

// unlinkMapped removes the link at relative from each mapped path, before
// the Directory's file goes or changes.
func (f *DirectoryFiles) unlinkMapped(mappings []domain.MappedPath, relative string) error {
	for _, mapped := range mappings {
		view, err := f.view(mapped)
		if err != nil {
			return err
		}
		if err := removeEntry(filepath.Join(view, filepath.FromSlash(relative))); err != nil {
			return err
		}
	}
	return nil
}

// linkMapped links the file at a position into each mapped path once it is
// in place. A link not made leaves the Mapped View behind, which the repair
// aligns; the change itself was carried out.
func (f *DirectoryFiles) linkMapped(mappings []domain.MappedPath, position domain.FilePosition) error {
	source := filepath.Join(f.folder(position.Directory), position.Name)
	var failures []error
	for _, mapped := range mappings {
		view, err := f.view(mapped)
		if err == nil {
			err = linkInto(view, position.Name, source)
		}
		if err != nil {
			failures = append(failures, oops.With("mapped_path", string(mapped)).Wrap(err))
		}
	}
	if len(failures) > 0 {
		return errors.Join(append(failures, application.ErrMappedViewBehind)...)
	}
	return nil
}

// clearMapped removes every file and folder from each mapped path, keeping
// its own folder, before the Directory's whole content is replaced.
func (f *DirectoryFiles) clearMapped(mappings []domain.MappedPath) error {
	for _, mapped := range mappings {
		view, err := f.view(mapped)
		if err != nil {
			return err
		}
		if err := pruneView(view, nil); err != nil {
			return err
		}
	}
	return nil
}

// PruneMappedView removes every entry of the Mapped View outside the mapped
// paths and the folders above them, then each such folder left empty.
func (f *DirectoryFiles) PruneMappedView(ctx context.Context, mapped []domain.MappedPath) error {
	if f.layout.MappedView == "" {
		return oops.With("operation", "storage.prune_mapped_view").Wrap(errNoMappedView)
	}
	root := filepath.Join(f.layout.Root, filepath.FromSlash(f.layout.MappedView))
	if !exists(root) {
		return nil
	}
	held := make(map[string]bool, len(mapped))
	above := map[string]bool{}
	for _, path := range mapped {
		held[string(path)] = true
		for parent := filepath.ToSlash(filepath.Dir(string(path))); parent != "."; parent = filepath.ToSlash(filepath.Dir(parent)) {
			above[parent] = true
		}
	}
	return oops.With("operation", "storage.prune_mapped_view").Wrap(pruneFolder(ctx, root, "", held, above))
}

// pruneFolder prunes the folder at relative beneath root: a mapped path is
// kept whole, a folder above one is pruned in turn and removed once empty,
// and anything else goes.
func pruneFolder(ctx context.Context, root, relative string, held, above map[string]bool) error {
	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(relative)))
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		child := path.Join(relative, entry.Name())
		full := filepath.Join(root, filepath.FromSlash(child))
		switch {
		case held[child]:
		case above[child] && entry.IsDir():
			if err := pruneFolder(ctx, root, child, held, above); err != nil {
				return err
			}
			if err := unix.Rmdir(full); err != nil && !errors.Is(err, unix.ENOTEMPTY) && !errors.Is(err, unix.EEXIST) && !errors.Is(err, unix.ENOENT) {
				return err
			}
		default:
			if err := os.RemoveAll(full); err != nil {
				return err
			}
		}
	}
	return nil
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
