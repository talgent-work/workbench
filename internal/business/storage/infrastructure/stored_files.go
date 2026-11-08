package infrastructure

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/samber/oops"
	"golang.org/x/sys/unix"

	"github.com/talgent-work/workbench/internal/business/storage/application"
	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

// The files Storage writes into a Directory: each lies directly in its
// folder under its name, carries its origin as extended attributes, and
// becomes visible by one atomic rename from the pending area, so readers
// never see a partial file.

// List lists the regular files directly in the Directory; an entry removed
// meanwhile, or one that is not a file name, is not a file of it.
func (f *DirectoryFiles) List(ctx context.Context, id domain.DirectoryID) ([]application.StoredFile, error) {
	fd, err := f.open(id)
	if errors.Is(err, unix.ENOENT) {
		return nil, nil
	}
	if err != nil {
		return nil, oops.With("operation", "storage.list", "directory", f.relative(id)).Wrap(err)
	}
	defer unix.Close(fd)
	names, err := directoryNames(fd)
	if err != nil {
		return nil, oops.With("operation", "storage.list", "directory", f.relative(id)).Wrap(err)
	}
	var files []application.StoredFile
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		position, err := domain.NewFilePosition(id, name)
		if err != nil {
			continue
		}
		file, err := statAt(fd, position)
		if errors.Is(err, application.ErrFileNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	return files, nil
}

func (f *DirectoryFiles) Stat(ctx context.Context, position domain.FilePosition) (application.StoredFile, error) {
	if err := ctx.Err(); err != nil {
		return application.StoredFile{}, err
	}
	fd, err := f.open(position.Directory)
	if errors.Is(err, unix.ENOENT) {
		return application.StoredFile{}, application.ErrFileNotFound
	}
	if err != nil {
		return application.StoredFile{}, oops.With("operation", "storage.stat", "directory", f.relative(position.Directory)).Wrap(err)
	}
	defer unix.Close(fd)
	return statAt(fd, position)
}

// Write stages the content, then commits it; staged content that cannot be
// committed is discarded, and staging sweeps what a crash leaves.
func (f *DirectoryFiles) Write(ctx context.Context, position domain.FilePosition, content io.Reader, attributes application.FileAttributes, replace bool) (application.StoredFile, error) {
	pending := f.private(pendingArea)
	staged, err := stage(ctx, f.layout.Root, pending, content, attributes, f.now())
	if err != nil {
		return application.StoredFile{}, err
	}
	stored, err := f.commit(ctx, staged, position, replace)
	if err != nil {
		discard(f.layout.Root, pending, staged)
		return application.StoredFile{}, err
	}
	return stored, nil
}

func (f *DirectoryFiles) commit(ctx context.Context, staged string, position domain.FilePosition, replace bool) (application.StoredFile, error) {
	if err := ctx.Err(); err != nil {
		return application.StoredFile{}, err
	}
	operation := oops.With("operation", "storage.commit", "directory", f.relative(position.Directory))
	pending, err := openDirectory(f.layout.Root, f.private(pendingArea), false, 0)
	if err != nil {
		return application.StoredFile{}, operation.Wrap(err)
	}
	defer unix.Close(pending)
	// The folder is never created here: a Directory reclaimed meanwhile
	// stays gone.
	target, err := f.open(position.Directory)
	if err != nil {
		return application.StoredFile{}, operation.Wrap(err)
	}
	defer unix.Close(target)
	if replace {
		// The exchange fails on an absent target, and the previous content
		// stays in place until the new one has taken its position.
		err = unix.Renameat2(pending, staged, target, position.Name, unix.RENAME_EXCHANGE)
		if errors.Is(err, unix.ENOENT) {
			return application.StoredFile{}, application.ErrFileNotFound
		}
		if err != nil {
			return application.StoredFile{}, operation.Wrap(err)
		}
		_ = unix.Unlinkat(pending, staged, 0)
	} else {
		err = unix.Renameat2(pending, staged, target, position.Name, unix.RENAME_NOREPLACE)
		if errors.Is(err, unix.EEXIST) {
			return application.StoredFile{}, application.ErrNameTaken
		}
		if err != nil {
			return application.StoredFile{}, operation.Wrap(err)
		}
	}
	if err := unix.Fsync(target); err != nil {
		return application.StoredFile{}, operation.Wrap(err)
	}
	return statAt(target, position)
}

func (f *DirectoryFiles) Rename(ctx context.Context, from, to domain.FilePosition) (application.StoredFile, error) {
	if err := ctx.Err(); err != nil {
		return application.StoredFile{}, err
	}
	operation := oops.With("operation", "storage.rename", "directory", f.relative(from.Directory))
	fd, err := f.open(from.Directory)
	if errors.Is(err, unix.ENOENT) {
		return application.StoredFile{}, application.ErrFileNotFound
	}
	if err != nil {
		return application.StoredFile{}, operation.Wrap(err)
	}
	defer unix.Close(fd)
	err = unix.Renameat2(fd, from.Name, fd, to.Name, unix.RENAME_NOREPLACE)
	switch {
	case errors.Is(err, unix.ENOENT):
		return application.StoredFile{}, application.ErrFileNotFound
	case errors.Is(err, unix.EEXIST):
		return application.StoredFile{}, application.ErrNameTaken
	case err != nil:
		return application.StoredFile{}, operation.Wrap(err)
	}
	if err := unix.Fsync(fd); err != nil {
		return application.StoredFile{}, operation.Wrap(err)
	}
	return statAt(fd, to)
}

func (f *DirectoryFiles) Remove(ctx context.Context, position domain.FilePosition) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	operation := oops.With("operation", "storage.remove", "directory", f.relative(position.Directory))
	fd, err := f.open(position.Directory)
	if errors.Is(err, unix.ENOENT) {
		return false, nil
	}
	if err != nil {
		return false, operation.Wrap(err)
	}
	defer unix.Close(fd)
	// Of concurrent deletions only the one whose unlink succeeds removed it.
	err = unix.Unlinkat(fd, position.Name, 0)
	if errors.Is(err, unix.ENOENT) {
		return false, nil
	}
	if err != nil {
		return false, operation.Wrap(err)
	}
	return true, nil
}

func (f *DirectoryFiles) Open(ctx context.Context, position domain.FilePosition) (application.OpenedFile, error) {
	if err := ctx.Err(); err != nil {
		return application.OpenedFile{}, err
	}
	operation := oops.With("operation", "storage.open", "directory", f.relative(position.Directory))
	dir, err := f.open(position.Directory)
	if errors.Is(err, unix.ENOENT) {
		return application.OpenedFile{}, application.ErrFileNotFound
	}
	if err != nil {
		return application.OpenedFile{}, operation.Wrap(err)
	}
	defer unix.Close(dir)
	fd, err := unix.Openat(dir, position.Name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if errors.Is(err, unix.ENOENT) || errors.Is(err, unix.ELOOP) {
		return application.OpenedFile{}, application.ErrFileNotFound
	}
	if err != nil {
		return application.OpenedFile{}, operation.Wrap(err)
	}
	stored, err := describe(fd, position)
	if err != nil {
		unix.Close(fd)
		return application.OpenedFile{}, err
	}
	return application.OpenedFile{Content: os.NewFile(uintptr(fd), position.Name), File: stored}, nil
}

// Inspect reads the facts Preview Policy judges from the file's current
// content, together with the file's own facts from the same open file.
func (f *DirectoryFiles) Inspect(ctx context.Context, position domain.FilePosition) (application.StoredFile, domain.ContentStructure, error) {
	opened, err := f.Open(ctx, position)
	if err != nil {
		return application.StoredFile{}, domain.ContentStructure{}, err
	}
	defer opened.Content.Close()
	reader, ok := opened.Content.(io.ReaderAt)
	if !ok {
		return application.StoredFile{}, domain.ContentStructure{}, oops.Errorf("storage: content is not randomly readable")
	}
	structure, err := inspectContent(reader, opened.File.SizeBytes)
	if err != nil {
		return application.StoredFile{}, domain.ContentStructure{}, oops.With("operation", "storage.inspect", "directory", f.relative(position.Directory)).Wrap(err)
	}
	return opened.File, structure, nil
}

// stage writes complete content into the pending area under a new name,
// with its attributes and digest, sweeping what earlier writers left there.
func stage(ctx context.Context, root, area string, content io.Reader, attributes application.FileAttributes, now time.Time) (string, error) {
	pending, err := openDirectory(root, area, true, privateMode)
	if err != nil {
		return "", oops.With("operation", "storage.stage").Wrap(err)
	}
	defer unix.Close(pending)
	sweepPending(pending, now)
	name := uuid.Must(uuid.NewV7()).String()
	fd, err := unix.Openat(pending, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, fileMode)
	if err != nil {
		return "", oops.With("operation", "storage.stage").Wrap(err)
	}
	file := os.NewFile(uintptr(fd), name)
	if err := writeStaged(ctx, file, content, attributes); err != nil {
		file.Close()
		_ = unix.Unlinkat(pending, name, 0)
		return "", err
	}
	if err := file.Close(); err != nil {
		_ = unix.Unlinkat(pending, name, 0)
		return "", oops.With("operation", "storage.stage").Wrap(err)
	}
	return name, nil
}

func writeStaged(ctx context.Context, file *os.File, content io.Reader, attributes application.FileAttributes) error {
	if err := file.Chmod(fileMode); err != nil {
		return oops.With("operation", "storage.stage").Wrap(err)
	}
	digest := sha256.New()
	if _, err := io.Copy(io.MultiWriter(file, digest), contextReader{ctx: ctx, reader: content}); err != nil {
		return oops.With("operation", "storage.stage").Wrap(err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	attributes.ContentDigest = hex.EncodeToString(digest.Sum(nil))
	if err := writeAttributes(int(file.Fd()), attributes); err != nil {
		return oops.With("operation", "storage.stage_attributes").Wrap(err)
	}
	return oops.With("operation", "storage.stage").Wrap(file.Sync())
}

// discard removes staged content at once; should that fail, the pending
// sweep removes it later.
func discard(root, area, staged string) {
	pending, err := openDirectory(root, area, false, 0)
	if err != nil {
		return
	}
	defer unix.Close(pending)
	_ = unix.Unlinkat(pending, staged, 0)
}

// sweepPending removes staged content older than stalePending, left by a
// process that ended mid-write. It is best effort: whatever it cannot remove
// now, a later write sweeps again.
func sweepPending(pending int, now time.Time) {
	names, err := directoryNames(pending)
	if err != nil {
		return
	}
	for _, name := range names {
		var stat unix.Stat_t
		if unix.Fstatat(pending, name, &stat, unix.AT_SYMLINK_NOFOLLOW) != nil {
			continue
		}
		if now.Sub(time.Unix(stat.Mtim.Unix())) >= stalePending {
			_ = unix.Unlinkat(pending, name, 0)
		}
	}
}

// statAt describes the regular file at a position of an open folder.
func statAt(directory int, position domain.FilePosition) (application.StoredFile, error) {
	fd, err := unix.Openat(directory, position.Name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if errors.Is(err, unix.ENOENT) || errors.Is(err, unix.ELOOP) {
		return application.StoredFile{}, application.ErrFileNotFound
	}
	if err != nil {
		return application.StoredFile{}, oops.With("operation", "storage.stat").Wrap(err)
	}
	defer unix.Close(fd)
	return describe(fd, position)
}

func describe(fd int, position domain.FilePosition) (application.StoredFile, error) {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return application.StoredFile{}, oops.With("operation", "storage.stat").Wrap(err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG {
		return application.StoredFile{}, application.ErrFileNotFound
	}
	attributes, err := readAttributes(fd)
	if err != nil {
		return application.StoredFile{}, oops.With("operation", "storage.read_attributes").Wrap(err)
	}
	return application.StoredFile{
		Position:   position,
		SizeBytes:  stat.Size,
		ModifiedAt: time.Unix(stat.Mtim.Unix()).UTC(),
		Attributes: attributes,
	}, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(buf []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buf)
}
