package infrastructure

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/samber/oops"
	"golang.org/x/sys/unix"

	"github.com/talgent-work/workbench/internal/business/storage/application"
	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

const (
	fileMode      = 0o644
	directoryMode = 0o755
	// privateMode keeps Storage's own areas and a sandbox's directories from
	// every other user of the file system.
	privateMode = 0o700
	// lockPoll is how often a waiting lock holder retries.
	lockPoll = 25 * time.Millisecond
)

// Areas of Storage's own beside the published Directories, under a
// realization's private path.
const (
	pendingArea  = "pending"
	locksArea    = "locks"
	removalArea  = "reclaimed"
	stagingArea  = "content-staging"
	retiredArea  = "content-retired"
	stalePending = time.Hour
)

// DirectoryLayout is where one file system keeps Directories: under Root,
// each at Published/<owner>/<name>, with Storage's own areas under Private,
// and how sandboxes reach them.
type DirectoryLayout struct {
	// Root is the controlplane's mount of the file system.
	Root string
	// Published holds every Directory, laid out by owner and name.
	Published string
	// Private holds staged content, locks and content being removed,
	// outside every Directory.
	Private string
	// MappedView holds the Mapped View, outside every Directory; empty on a
	// file system that keeps none.
	MappedView string
	// StandingPaths are the mapped paths that standing readers' volumes
	// show: their folders always exist, emptied or not.
	StandingPaths []domain.MappedPath
	// Claim is the shared volume claim sandboxes mount Directories from, and
	// ClaimRoot where Root lies within it.
	Claim, ClaimRoot string
	// SandboxUID owns every mount point Storage prepares, and every
	// Directory when SandboxWrites; a negative value keeps the creating user.
	SandboxUID int
	// SandboxWrites gives each new Directory to the sandbox user, for a file
	// system Harness processes write through read-write mounts.
	SandboxWrites bool
}

// DirectoryFiles keeps Directories on one file system, the realization of
// DirectoryBackendPort a Directory's Backend selects. Nothing a sandbox
// placed is ever followed: every path is opened beneath its Directory
// without symbolic links.
type DirectoryFiles struct {
	layout DirectoryLayout
	now    func() time.Time
}

var _ application.DirectoryBackendPort = (*DirectoryFiles)(nil)

func NewDirectoryFiles(layout DirectoryLayout) *DirectoryFiles {
	return &DirectoryFiles{layout: layout, now: time.Now}
}

// ownerKey is the owner's one path element.
func ownerKey(owner domain.DirectoryOwner) string {
	return OwnerElement(owner.Facts())
}

// relative is a Directory's folder relative to Root.
func (f *DirectoryFiles) relative(id domain.DirectoryID) string {
	return path.Join(f.layout.Published, ownerKey(id.Owner), id.Name)
}

// key names a Directory as one file name in Storage's own areas.
func key(id domain.DirectoryID) string {
	digest := sha256.Sum256([]byte(ownerKey(id.Owner) + "/" + id.Name))
	return hex.EncodeToString(digest[:])
}

func (f *DirectoryFiles) private(area string) string { return path.Join(f.layout.Private, area) }

// open opens a Directory's folder; ENOENT means its space does not exist.
func (f *DirectoryFiles) open(id domain.DirectoryID) (int, error) {
	return openDirectory(f.layout.Root, f.relative(id), false, 0)
}

func (f *DirectoryFiles) directoryMode() uint32 {
	if f.layout.SandboxWrites {
		return privateMode
	}
	return directoryMode
}

func (f *DirectoryFiles) Allocate(ctx context.Context, id domain.DirectoryID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	parent, err := openDirectory(f.layout.Root, path.Join(f.layout.Published, ownerKey(id.Owner)), true, directoryMode)
	if err != nil {
		return oops.With("operation", "storage.allocate", "directory", f.relative(id)).Wrap(err)
	}
	defer unix.Close(parent)
	if err := unix.Mkdirat(parent, id.Name, f.directoryMode()); err != nil && !errors.Is(err, unix.EEXIST) {
		return oops.With("operation", "storage.allocate", "directory", f.relative(id)).Wrap(err)
	}
	if f.layout.SandboxWrites {
		if err := f.own(parent, id.Name); err != nil {
			return oops.With("operation", "storage.allocate", "directory", f.relative(id)).Wrap(err)
		}
	}
	return oops.With("operation", "storage.allocate").Wrap(unix.Fsync(parent))
}

// own gives an entry to the sandbox user, never following a link.
func (f *DirectoryFiles) own(parent int, name string) error {
	if f.layout.SandboxUID < 0 {
		return nil
	}
	return unix.Fchownat(parent, name, f.layout.SandboxUID, f.layout.SandboxUID, unix.AT_SYMLINK_NOFOLLOW)
}

// PrepareMountPoints creates each mount point with every parent and gives
// each of them to the sandbox user, so no mount leaves a directory owned by
// anyone else in the Directory. Existing content is never reset; a link in
// the way refuses the preparation.
func (f *DirectoryFiles) PrepareMountPoints(ctx context.Context, id domain.DirectoryID, points []domain.MountPoint) error {
	if len(points) == 0 {
		return nil
	}
	root, err := f.open(id)
	if err != nil {
		return oops.With("operation", "storage.prepare_mount_points", "directory", f.relative(id)).Wrap(err)
	}
	defer unix.Close(root)
	for _, point := range points {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := f.prepare(root, string(point)); err != nil {
			return oops.With("operation", "storage.prepare_mount_points", "directory", f.relative(id), "mount_point", string(point)).Wrap(err)
		}
	}
	return nil
}

func (f *DirectoryFiles) prepare(root int, point string) error {
	fd, err := unix.Dup(root)
	if err != nil {
		return err
	}
	for _, part := range strings.Split(point, "/") {
		if err := unix.Mkdirat(fd, part, f.directoryMode()); err != nil && !errors.Is(err, unix.EEXIST) {
			unix.Close(fd)
			return err
		}
		if err := f.own(fd, part); err != nil {
			unix.Close(fd)
			return err
		}
		next, err := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(fd)
		if err != nil {
			return err
		}
		fd = next
	}
	return unix.Close(fd)
}

func (f *DirectoryFiles) Locate(id domain.DirectoryID) application.MountLocation {
	return application.MountLocation{ClaimName: f.layout.Claim, SubPath: path.Join(f.layout.ClaimRoot, f.relative(id))}
}

// CopyPublicationSource holds the Directory's lock shared while read copies
// the source, so its removal waits for the copy.
func (f *DirectoryFiles) CopyPublicationSource(ctx context.Context, id domain.DirectoryID, source domain.WorkspacePath, read func(io.Reader) error) error {
	unlock, err := f.lock(ctx, id, unix.LOCK_SH)
	if err != nil {
		return err
	}
	defer unlock()
	root, err := f.open(id)
	if errors.Is(err, unix.ENOENT) {
		return application.ErrSourceNotFound
	}
	if err != nil {
		return oops.With("operation", "storage.publication_source").Wrap(err)
	}
	defer unix.Close(root)
	fd, err := openBeneath(root, string(source), unix.O_RDONLY|unix.O_NONBLOCK)
	switch {
	case errors.Is(err, errBeneathMissing):
		return application.ErrSourceNotFound
	case errors.Is(err, errBeneathRefused):
		return application.ErrInvalidSource
	case err != nil:
		return err
	}
	file := os.NewFile(uintptr(fd), string(source))
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return oops.With("operation", "storage.publication_source").Wrap(err)
	}
	if !info.Mode().IsRegular() {
		return application.ErrInvalidSource
	}
	return read(file)
}

// RemoveReclaimed detaches the Directory's folder into the removal area once
// no publication holds its lock, then removes it. A detached folder left by
// an interrupted attempt is removed by the next one.
func (f *DirectoryFiles) RemoveReclaimed(ctx context.Context, id domain.DirectoryID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	unlock, err := f.tryLock(id)
	if errors.Is(err, unix.EWOULDBLOCK) {
		return application.ErrPublicationInProgress
	}
	if err != nil {
		return err
	}
	defer unlock()
	removal, err := openDirectory(f.layout.Root, f.private(removalArea), true, privateMode)
	if err != nil {
		return oops.With("operation", "storage.remove_reclaimed", "directory", f.relative(id)).Wrap(err)
	}
	unix.Close(removal)
	detached := filepath.Join(f.layout.Root, filepath.FromSlash(f.private(removalArea)), key(id))
	if err := os.RemoveAll(detached); err != nil {
		return oops.With("operation", "storage.remove_reclaimed", "directory", f.relative(id)).Wrap(err)
	}
	folder := filepath.Join(f.layout.Root, filepath.FromSlash(f.relative(id)))
	if err := os.Rename(folder, detached); err != nil && !errors.Is(err, os.ErrNotExist) {
		return oops.With("operation", "storage.remove_reclaimed", "directory", f.relative(id)).Wrap(err)
	}
	var failures []error
	for _, leftover := range []string{detached, f.areaPath(stagingArea, id), f.areaPath(retiredArea, id)} {
		if err := os.RemoveAll(leftover); err != nil {
			failures = append(failures, err)
		}
	}
	// A publication waiting on the lock file finds no folder once it gets
	// it; nothing locks a reclaimed Directory again.
	if err := os.Remove(f.areaPath(locksArea, id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		failures = append(failures, err)
	}
	// The owner's folder goes with its last Directory, so a reclaimed Work
	// leaves nothing behind. An allocation that opened it a moment before
	// fails to create its Directory and is completed by the next opening.
	owner := filepath.Dir(folder)
	if err := unix.Rmdir(owner); err != nil && !errors.Is(err, unix.ENOTEMPTY) && !errors.Is(err, unix.EEXIST) && !errors.Is(err, unix.ENOENT) {
		failures = append(failures, err)
	}
	return oops.With("operation", "storage.remove_reclaimed", "directory", f.relative(id)).Wrap(errors.Join(failures...))
}

// areaPath is the Directory's entry in one of Storage's own areas.
func (f *DirectoryFiles) areaPath(area string, id domain.DirectoryID) string {
	return filepath.Join(f.layout.Root, filepath.FromSlash(f.private(area)), key(id))
}

// lock takes the Directory's lock in mode, waiting while another holder, in
// this or any other process on the file system, keeps it.
func (f *DirectoryFiles) lock(ctx context.Context, id domain.DirectoryID, mode int) (func(), error) {
	return f.lockNamed(ctx, key(id), mode)
}

// lockNamed takes the lock of the name in the locks area in mode, waiting
// while another holder keeps it.
func (f *DirectoryFiles) lockNamed(ctx context.Context, name string, mode int) (func(), error) {
	fd, err := f.openLock(name)
	if err != nil {
		return nil, err
	}
	for {
		err := unix.Flock(fd, mode|unix.LOCK_NB)
		if err == nil {
			return releaser(fd), nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) {
			unix.Close(fd)
			return nil, oops.With("operation", "storage.lock").Wrap(err)
		}
		select {
		case <-ctx.Done():
			unix.Close(fd)
			return nil, ctx.Err()
		case <-time.After(lockPoll):
		}
	}
}

// tryLock takes the Directory's lock exclusively or reports EWOULDBLOCK.
func (f *DirectoryFiles) tryLock(id domain.DirectoryID) (func(), error) {
	fd, err := f.openLock(key(id))
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		unix.Close(fd)
		return nil, err
	}
	return releaser(fd), nil
}

func releaser(fd int) func() {
	return func() { _ = unix.Flock(fd, unix.LOCK_UN); unix.Close(fd) }
}

func (f *DirectoryFiles) openLock(name string) (int, error) {
	locks, err := openDirectory(f.layout.Root, f.private(locksArea), true, privateMode)
	if err != nil {
		return -1, oops.With("operation", "storage.lock").Wrap(err)
	}
	defer unix.Close(locks)
	fd, err := unix.Openat(locks, name, unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return -1, oops.With("operation", "storage.lock").Wrap(err)
	}
	return fd, nil
}
