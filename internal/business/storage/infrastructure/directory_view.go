package infrastructure

import (
	"errors"
	"io"
	"os"
	"time"

	"github.com/samber/oops"
	"golang.org/x/sys/unix"

	"github.com/talgent-work/workbench/internal/business/storage/application"
	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

// Reading a directory tree for the Workspace View, shared by Session
// Workspaces and Reference Spaces: every path stays beneath its root, and a
// symbolic link is listed but never followed.

// openBeneath opens a path strictly inside dir, refusing every symbolic link
// and any crossing into another mount.
// Outcomes of openBeneath its callers name in their own terms.
var (
	errBeneathMissing = errors.New("storage: no entry at the path")
	errBeneathRefused = errors.New("storage: the path crosses a link or a mount")
)

func openBeneath(dir int, relative string, flags int) (int, error) {
	fd, err := unix.Openat2(dir, relative, &unix.OpenHow{
		Flags:   uint64(flags | unix.O_CLOEXEC | unix.O_NOFOLLOW),
		Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS | unix.RESOLVE_NO_XDEV,
	})
	switch {
	case errors.Is(err, unix.ENOENT), errors.Is(err, unix.ENOTDIR):
		return -1, errBeneathMissing
	case errors.Is(err, unix.ELOOP), errors.Is(err, unix.EXDEV):
		return -1, errBeneathRefused
	case err != nil:
		return -1, oops.With("operation", "storage.open_beneath").Wrap(err)
	}
	return fd, nil
}

// listBeneath lists the first limit entries of a directory beneath root by
// name, with its entry count; "." is root itself. An entry hidden reports
// true is left out.
func listBeneath(root int, directory string, limit int, hidden func(string) bool) (application.WorkspaceListing, error) {
	fd := root
	if directory != "." {
		var err error
		fd, err = openBeneath(root, directory, unix.O_RDONLY|unix.O_DIRECTORY)
		if errors.Is(err, errBeneathMissing) {
			return application.WorkspaceListing{}, application.ErrFileNotFound
		}
		if errors.Is(err, errBeneathRefused) {
			return application.WorkspaceListing{}, application.ErrInvalidWorkspacePath
		}
		if err != nil {
			return application.WorkspaceListing{}, err
		}
		defer unix.Close(fd)
	}
	names, err := directoryNames(fd)
	if err != nil {
		return application.WorkspaceListing{}, oops.With("operation", "storage.workspace_view").Wrap(err)
	}
	if hidden != nil {
		kept := names[:0]
		for _, name := range names {
			if !hidden(name) {
				kept = append(kept, name)
			}
		}
		names = kept
	}
	listing := application.WorkspaceListing{Total: len(names)}
	for _, name := range names {
		if len(listing.Entries) == limit {
			break
		}
		var stat unix.Stat_t
		if err := unix.Fstatat(fd, name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			// The writer keeps writing: an entry removed since the listing
			// is simply no longer there.
			if errors.Is(err, unix.ENOENT) {
				listing.Total--
				continue
			}
			return application.WorkspaceListing{}, oops.With("operation", "storage.workspace_view").Wrap(err)
		}
		entry := application.WorkspaceEntry{Name: name, Kind: application.WorkspaceFile, ModifiedAt: time.Unix(stat.Mtim.Unix()).UTC()}
		switch stat.Mode & unix.S_IFMT {
		case unix.S_IFDIR:
			entry.Kind = application.WorkspaceDirectory
		case unix.S_IFLNK:
			entry.Kind = application.WorkspaceLink
		default:
			entry.SizeBytes = stat.Size
		}
		listing.Entries = append(listing.Entries, entry)
	}
	return listing, nil
}

// openRegularBeneath opens one regular file beneath root; links, directories
// and devices are not files a reader opens. What is read is the content of
// that inode when opened.
func openRegularBeneath(root int, file string) (application.OpenedWorkspaceFile, error) {
	fd, err := openBeneath(root, file, unix.O_RDONLY|unix.O_NONBLOCK)
	switch {
	case errors.Is(err, errBeneathMissing):
		return application.OpenedWorkspaceFile{}, application.ErrFileNotFound
	case errors.Is(err, errBeneathRefused):
		return application.OpenedWorkspaceFile{}, application.ErrInvalidWorkspacePath
	case err != nil:
		return application.OpenedWorkspaceFile{}, err
	}
	opened := os.NewFile(uintptr(fd), file)
	info, err := opened.Stat()
	if err != nil {
		opened.Close()
		return application.OpenedWorkspaceFile{}, oops.With("operation", "storage.workspace_file").Wrap(err)
	}
	if !info.Mode().IsRegular() {
		opened.Close()
		return application.OpenedWorkspaceFile{}, application.ErrInvalidWorkspacePath
	}
	return application.OpenedWorkspaceFile{Content: opened, SizeBytes: info.Size(), ModifiedAt: info.ModTime().UTC()}, nil
}

// inspectOpened reads the facts Preview Policy judges from an opened file,
// closing it.
func inspectOpened(opened application.OpenedWorkspaceFile) (application.WorkspaceFileVersion, domain.ContentStructure, error) {
	defer opened.Content.Close()
	reader, ok := opened.Content.(io.ReaderAt)
	if !ok {
		return application.WorkspaceFileVersion{}, domain.ContentStructure{}, oops.Errorf("storage: workspace content is not randomly readable")
	}
	structure, err := inspectContent(reader, opened.SizeBytes)
	if err != nil {
		return application.WorkspaceFileVersion{}, domain.ContentStructure{}, oops.With("operation", "storage.inspect_workspace_file").Wrap(err)
	}
	return application.WorkspaceFileVersion{SizeBytes: opened.SizeBytes, ModifiedAt: opened.ModifiedAt}, structure, nil
}
