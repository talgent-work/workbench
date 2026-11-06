package infrastructure

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"time"

	"github.com/google/uuid"
	"github.com/samber/oops"
	"golang.org/x/sys/unix"

	"github.com/talgent-work/workbench/internal/business/storage/application"
	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

// Working storage layout under the NAS export root.
const (
	workspacesDirectory = "workspaces"
	homesDirectory      = "homes"
	reclaimDirectory    = ".reclaim"
	requestsDirectory   = ".reclaim/requests"
	// reclaimedDirectory keeps one empty record per reclaimed workspace, so
	// a reader learns it was reclaimed rather than never created. Records are
	// one empty file per closed Work and are kept, as a Closed Work's page
	// stays readable.
	reclaimedDirectory = ".reclaim/reclaimed"
	// locksDirectory holds one lock file per workspace: publications copying
	// from it hold it shared, reclamation exclusively.
	locksDirectory = ".locks"
	// lockPoll is how often a publication retries a lock reclamation holds.
	lockPoll = 25 * time.Millisecond
)

// WorkingFiles keeps Session Workspaces and HOMEs on the NAS. Directories are
// created and reclaimed only here; sandboxes write content inside their own
// workspace and HOME, so nothing a sandbox placed is ever followed.
type WorkingFiles struct {
	root string
	// owner is the sandbox user every new workspace and HOME belongs to;
	// a negative value keeps the creating user.
	owner int
}

var _ application.WorkingFilesPort = (*WorkingFiles)(nil)

func NewWorkingFiles(root string, sandboxUID int) *WorkingFiles {
	return &WorkingFiles{root: root, owner: sandboxUID}
}

// spaceKey names a workspace's directory by its reference.
func spaceKey(reference string) string {
	digest := sha256.Sum256([]byte(reference))
	return hex.EncodeToString(digest[:])
}

func (w *WorkingFiles) PrepareWorkspace(ctx context.Context, reference string, mounts []string) (string, error) {
	directory := path.Join(workspacesDirectory, spaceKey(reference))
	if err := w.prepare(ctx, directory); err != nil {
		return "", err
	}
	// Each mount point stays an empty directory on the NAS: the sandbox sees
	// the mounted space there and can never write beneath it.
	for _, mountPoint := range append([]string{domain.InputsDirectory}, mounts...) {
		if err := w.prepare(ctx, path.Join(directory, mountPoint)); err != nil {
			return "", err
		}
	}
	return directory, nil
}

func (w *WorkingFiles) PrepareHome(ctx context.Context, ownerKey string) (string, error) {
	directory := path.Join(homesDirectory, ownerKey)
	return directory, w.prepare(ctx, directory)
}

// prepare creates the directory when missing and gives it to the sandbox
// user. Retained content is never reset or recursively changed.
func (w *WorkingFiles) prepare(ctx context.Context, directory string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	parent, err := openDirectory(w.root, path.Dir(directory), true, directoryMode)
	if err != nil {
		return oops.With("operation", "storage.prepare", "directory", directory).Wrap(err)
	}
	defer unix.Close(parent)
	name := path.Base(directory)
	if err := unix.Mkdirat(parent, name, 0o700); err != nil && !errors.Is(err, unix.EEXIST) {
		return oops.With("operation", "storage.prepare", "directory", directory).Wrap(err)
	}
	if w.owner >= 0 {
		if err := unix.Fchownat(parent, name, w.owner, w.owner, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return oops.With("operation", "storage.own", "directory", directory).Wrap(err)
		}
	}
	return nil
}

func (w *WorkingFiles) ReadPublicationSource(ctx context.Context, reference string, source domain.WorkspacePath, read func(io.Reader) error) error {
	clean := string(source)
	key := spaceKey(reference)
	unlock, err := w.lock(ctx, key, unix.LOCK_SH)
	if err != nil {
		return err
	}
	defer unlock()
	workspace, err := openDirectory(w.root, path.Join(workspacesDirectory, key), false, 0)
	if errors.Is(err, unix.ENOENT) {
		return application.ErrSourceNotFound
	}
	if err != nil {
		return oops.With("operation", "storage.publication_source").Wrap(err)
	}
	defer unix.Close(workspace)
	fd, err := openBeneath(workspace, clean, unix.O_RDONLY|unix.O_NONBLOCK)
	switch {
	case errors.Is(err, errBeneathMissing):
		return application.ErrSourceNotFound
	case errors.Is(err, errBeneathRefused):
		return application.ErrInvalidSource
	case err != nil:
		return err
	}
	file := os.NewFile(uintptr(fd), clean)
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

func (w *WorkingFiles) List(ctx context.Context, reference string, directory domain.WorkspacePath, limit int) (application.WorkspaceListing, error) {
	if err := ctx.Err(); err != nil {
		return application.WorkspaceListing{}, err
	}
	workspace, err := w.openWorkspace(reference)
	if err != nil {
		return application.WorkspaceListing{}, err
	}
	defer unix.Close(workspace)
	return listBeneath(workspace, string(directory), limit, nil)
}

// OpenFile opens one regular workspace file.
func (w *WorkingFiles) OpenFile(ctx context.Context, reference string, file domain.WorkspacePath) (application.OpenedWorkspaceFile, error) {
	if err := ctx.Err(); err != nil {
		return application.OpenedWorkspaceFile{}, err
	}
	workspace, err := w.openWorkspace(reference)
	if err != nil {
		return application.OpenedWorkspaceFile{}, err
	}
	defer unix.Close(workspace)
	return openRegularBeneath(workspace, string(file))
}

// openWorkspace opens a Work's workspace; a missing one was either never
// created or reclaimed, which its reclamation record tells.
func (w *WorkingFiles) openWorkspace(reference string) (int, error) {
	key := spaceKey(reference)
	workspace, err := openDirectory(w.root, path.Join(workspacesDirectory, key), false, 0)
	if errors.Is(err, unix.ENOENT) {
		return -1, w.missingWorkspace(key)
	}
	if err != nil {
		return -1, oops.With("operation", "storage.workspace").Wrap(err)
	}
	return workspace, nil
}

func (w *WorkingFiles) missingWorkspace(key string) error {
	for _, record := range []string{requestsDirectory, reclaimedDirectory} {
		dir, err := openDirectory(w.root, record, false, 0)
		if errors.Is(err, unix.ENOENT) {
			continue
		}
		if err != nil {
			return oops.With("operation", "storage.workspace").Wrap(err)
		}
		var stat unix.Stat_t
		err = unix.Fstatat(dir, key, &stat, unix.AT_SYMLINK_NOFOLLOW)
		unix.Close(dir)
		if err == nil {
			return application.ErrWorkspaceReclaimed
		}
		if !errors.Is(err, unix.ENOENT) {
			return oops.With("operation", "storage.workspace").Wrap(err)
		}
	}
	return application.ErrWorkspaceNotCreated
}

func (w *WorkingFiles) RequestReclamation(ctx context.Context, reference string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	requests, err := openDirectory(w.root, requestsDirectory, true, 0o700)
	if err != nil {
		return oops.With("operation", "storage.request_reclamation").Wrap(err)
	}
	defer unix.Close(requests)
	fd, err := unix.Openat(requests, spaceKey(reference), unix.O_WRONLY|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return oops.With("operation", "storage.request_reclamation").Wrap(err)
	}
	unix.Close(fd)
	return oops.With("operation", "storage.request_reclamation").Wrap(unix.Fsync(requests))
}

func (w *WorkingFiles) ReclaimRequested(ctx context.Context) error {
	requests, err := openDirectory(w.root, requestsDirectory, true, 0o700)
	if err != nil {
		return oops.With("operation", "storage.reclaim").Wrap(err)
	}
	defer unix.Close(requests)
	keys, err := directoryNames(requests)
	if err != nil {
		return oops.With("operation", "storage.reclaim").Wrap(err)
	}
	var failures []error
	for _, key := range keys {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := w.detachWorkspace(requests, key); err != nil {
			failures = append(failures, err)
		}
	}
	failures = append(failures, w.removeDetached(ctx))
	return errors.Join(failures...)
}

// detachWorkspace moves a requested workspace into the reclamation area once
// no publication copies from it, then forgets the request. A workspace still
// being copied from stays for a later attempt.
func (w *WorkingFiles) detachWorkspace(requests int, key string) error {
	unlock, err := w.tryLock(key)
	if errors.Is(err, unix.EWOULDBLOCK) {
		return nil
	}
	if err != nil {
		return err
	}
	defer unlock()
	workspaces, err := openDirectory(w.root, workspacesDirectory, true, directoryMode)
	if err != nil {
		return oops.With("operation", "storage.reclaim", "workspace", key).Wrap(err)
	}
	defer unix.Close(workspaces)
	reclaim, err := openDirectory(w.root, reclaimDirectory, true, 0o700)
	if err != nil {
		return oops.With("operation", "storage.reclaim", "workspace", key).Wrap(err)
	}
	defer unix.Close(reclaim)
	// Only a workspace that exists is recorded reclaimed, and before it is
	// detached: a crash between the two leaves the record and the request,
	// whose retry then detaches it. One never created stays so.
	var stat unix.Stat_t
	switch err := unix.Fstatat(workspaces, key, &stat, unix.AT_SYMLINK_NOFOLLOW); {
	case err == nil:
		if err := recordReclaimed(w.root, key); err != nil {
			return oops.With("operation", "storage.reclaim", "workspace", key).Wrap(err)
		}
	case !errors.Is(err, unix.ENOENT):
		return oops.With("operation", "storage.reclaim", "workspace", key).Wrap(err)
	}
	err = unix.Renameat(workspaces, key, reclaim, key+"."+uuid.Must(uuid.NewV7()).String())
	if err != nil && !errors.Is(err, unix.ENOENT) {
		return oops.With("operation", "storage.reclaim", "workspace", key).Wrap(err)
	}
	if err := unix.Unlinkat(requests, key, 0); err != nil && !errors.Is(err, unix.ENOENT) {
		return oops.With("operation", "storage.reclaim", "workspace", key).Wrap(err)
	}
	// A publication waiting on the old lock file finds no workspace once it
	// gets it. A later lock opens a new file, which is safe because a
	// reference names one Work's workspace and a Closed Work never starts
	// again, so nothing prepares this workspace anew.
	if locks, err := openDirectory(w.root, locksDirectory, false, 0); err == nil {
		_ = unix.Unlinkat(locks, key, 0)
		unix.Close(locks)
	}
	return nil
}

// recordReclaimed keeps that the workspace was reclaimed; it is recorded
// before the request is forgotten, so one of the two always says so.
func recordReclaimed(root, key string) error {
	reclaimed, err := openDirectory(root, reclaimedDirectory, true, 0o700)
	if err != nil {
		return err
	}
	defer unix.Close(reclaimed)
	fd, err := unix.Openat(reclaimed, key, unix.O_WRONLY|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return err
	}
	return unix.Close(fd)
}

// removeDetached deletes every detached workspace; what it cannot delete now
// the next attempt deletes.
func (w *WorkingFiles) removeDetached(ctx context.Context) error {
	reclaim, err := openDirectory(w.root, reclaimDirectory, true, 0o700)
	if err != nil {
		return oops.With("operation", "storage.reclaim_remove").Wrap(err)
	}
	names, err := directoryNames(reclaim)
	if err != nil {
		unix.Close(reclaim)
		return oops.With("operation", "storage.reclaim_remove").Wrap(err)
	}
	root, err := os.OpenRoot(fmt.Sprintf("/proc/self/fd/%d", reclaim))
	unix.Close(reclaim)
	if err != nil {
		return oops.With("operation", "storage.reclaim_remove").Wrap(err)
	}
	defer root.Close()
	var failures []error
	for _, name := range names {
		// The request and reclaimed records live beside the detached
		// workspaces and stay.
		if name == path.Base(requestsDirectory) || name == path.Base(reclaimedDirectory) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := root.RemoveAll(name); err != nil {
			failures = append(failures, oops.With("operation", "storage.reclaim_remove", "entry", name).Wrap(err))
		}
	}
	return errors.Join(failures...)
}

// InspectFile reads the facts Preview Policy judges from one regular file.
func (w *WorkingFiles) InspectFile(ctx context.Context, reference string, file domain.WorkspacePath) (application.WorkspaceFileVersion, domain.ContentStructure, error) {
	opened, err := w.OpenFile(ctx, reference, file)
	if err != nil {
		return application.WorkspaceFileVersion{}, domain.ContentStructure{}, err
	}
	return inspectOpened(opened)
}
