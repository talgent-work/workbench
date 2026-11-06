package infrastructure

import (
	"context"
	"errors"
	"time"

	"github.com/samber/oops"
	"golang.org/x/sys/unix"
)

// lock takes the workspace's lock in mode, waiting while reclamation holds it.
func (w *WorkingFiles) lock(ctx context.Context, key string, mode int) (func(), error) {
	return lockIn(ctx, w.root, locksDirectory, key, mode)
}

// lockIn takes the lock file key under directory in mode, waiting while
// another holder, in this or any other process on the volume, keeps it.
func lockIn(ctx context.Context, root, directory, key string, mode int) (func(), error) {
	fd, err := openLock(root, directory, key)
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

// tryLock takes the workspace's lock exclusively or reports EWOULDBLOCK.
func (w *WorkingFiles) tryLock(key string) (func(), error) {
	fd, err := openLock(w.root, locksDirectory, key)
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

func openLock(root, directory, key string) (int, error) {
	locks, err := openDirectory(root, directory, true, 0o700)
	if err != nil {
		return -1, oops.With("operation", "storage.lock").Wrap(err)
	}
	defer unix.Close(locks)
	fd, err := unix.Openat(locks, key, unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return -1, oops.With("operation", "storage.lock").Wrap(err)
	}
	return fd, nil
}
