package infrastructure

import (
	"context"
	"errors"
	"os"

	"github.com/google/uuid"
	"github.com/samber/oops"
	"golang.org/x/sys/unix"

	"github.com/talgent-work/workbench/internal/business/storage/application"
	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

// Each file Storage writes keeps its File ID with itself, as an extended
// attribute: staging gives new content a new one, so a Create holds a File
// ID no earlier file had, and a Replace writes the replaced file's onto the
// content that takes its place. A Share Link finds its file by reading the
// File ID of each file in the Directory.

// attributeFileID is the file's File ID, which Share Links reach. A rename
// and a hard link keep it with the inode; a Replace writes it onto the new
// content before it takes the position.
const attributeFileID = "user.talgent.file_id"

// newFileID is a File ID no file has had.
func newFileID() domain.FileID { return domain.FileID(uuid.Must(uuid.NewV7()).String()) }

// readFileID is the File ID kept with an open file, empty when it has none.
func readFileID(fd int) (domain.FileID, error) {
	buf := make([]byte, maxAttributeBytes)
	n, err := unix.Fgetxattr(fd, attributeFileID, buf)
	switch {
	case err == nil:
		return domain.FileID(buf[:n]), nil
	case errors.Is(err, unix.ENODATA), errors.Is(err, unix.ENOTSUP):
		return "", nil
	default:
		return "", err
	}
}

// writeFileID keeps the File ID with an open file; flags as for setxattr(2),
// so unix.XATTR_CREATE refuses a file that has one with EEXIST.
func writeFileID(fd int, file domain.FileID, flags int) error {
	return unix.Fsetxattr(fd, attributeFileID, []byte(file), flags)
}

// carryFileID writes the File ID of the file a Replace replaces onto the
// staged content, so the file keeps it. A replaced file without one leaves
// the staged content its new one.
func carryFileID(target int, name string, pending int, staged string) error {
	operation := oops.With("operation", "storage.carry_file_id")
	replaced, err := unix.Openat(target, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if errors.Is(err, unix.ENOENT) || errors.Is(err, unix.ELOOP) {
		return application.ErrFileNotFound
	}
	if err != nil {
		return operation.Wrap(err)
	}
	file, err := readFileID(replaced)
	unix.Close(replaced)
	if err != nil || file == "" {
		return operation.Wrap(err)
	}
	content, err := unix.Openat(pending, staged, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return operation.Wrap(err)
	}
	defer unix.Close(content)
	return operation.Wrap(writeFileID(content, file, 0))
}

// IdentifyFile is the File ID of the file at the position. A file Storage
// wrote before it kept File IDs gets a new one; of concurrent callers, the
// one whose exclusive write succeeds gives it, and the others read it.
func (f *DirectoryFiles) IdentifyFile(ctx context.Context, position domain.FilePosition) (domain.FileID, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	operation := oops.With("operation", "storage.identify_file", "directory", f.relative(position.Directory))
	dir, err := f.open(position.Directory)
	if errors.Is(err, unix.ENOENT) {
		return "", application.ErrFileNotFound
	}
	if err != nil {
		return "", operation.Wrap(err)
	}
	defer unix.Close(dir)
	fd, err := unix.Openat(dir, position.Name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if errors.Is(err, unix.ENOENT) || errors.Is(err, unix.ELOOP) {
		return "", application.ErrFileNotFound
	}
	if err != nil {
		return "", operation.Wrap(err)
	}
	defer unix.Close(fd)
	stored, err := describe(fd, position)
	if err != nil || stored.FileID != "" {
		return stored.FileID, err
	}
	given := newFileID()
	err = writeFileID(fd, given, unix.XATTR_CREATE)
	if errors.Is(err, unix.EEXIST) {
		file, err := readFileID(fd)
		return file, operation.Wrap(err)
	}
	if err != nil {
		return "", operation.Wrap(err)
	}
	return given, nil
}

// Find opens the file with the File ID directly in the Directory. Each
// file's File ID is read from its own open file, so the file returned is the
// one that holds it.
func (f *DirectoryFiles) Find(ctx context.Context, id domain.DirectoryID, file domain.FileID) (application.OpenedFile, error) {
	if file == "" {
		return application.OpenedFile{}, application.ErrFileNotFound
	}
	operation := oops.With("operation", "storage.find", "directory", f.relative(id))
	dir, err := f.open(id)
	if errors.Is(err, unix.ENOENT) {
		return application.OpenedFile{}, application.ErrFileNotFound
	}
	if err != nil {
		return application.OpenedFile{}, operation.Wrap(err)
	}
	defer unix.Close(dir)
	names, err := directoryNames(dir)
	if err != nil {
		return application.OpenedFile{}, operation.Wrap(err)
	}
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return application.OpenedFile{}, err
		}
		position, err := domain.NewFilePosition(id, name)
		if err != nil {
			continue
		}
		opened, found, err := openIdentified(dir, position, file)
		if err != nil {
			return application.OpenedFile{}, operation.Wrap(err)
		}
		if found {
			return opened, nil
		}
	}
	return application.OpenedFile{}, application.ErrFileNotFound
}

// openIdentified opens the regular file at the position when it holds the
// File ID; an entry gone meanwhile, one Storage cannot open, or another file
// is not found, so no single entry keeps the Directory's links from their
// files.
func openIdentified(dir int, position domain.FilePosition, file domain.FileID) (application.OpenedFile, bool, error) {
	fd, err := unix.Openat(dir, position.Name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if errors.Is(err, unix.ENOENT) || errors.Is(err, unix.ELOOP) || errors.Is(err, unix.EACCES) {
		return application.OpenedFile{}, false, nil
	}
	if err != nil {
		return application.OpenedFile{}, false, err
	}
	held, err := readFileID(fd)
	if err != nil || held != file {
		unix.Close(fd)
		return application.OpenedFile{}, false, err
	}
	stored, err := describe(fd, position)
	if errors.Is(err, application.ErrFileNotFound) {
		unix.Close(fd)
		return application.OpenedFile{}, false, nil
	}
	if err != nil {
		unix.Close(fd)
		return application.OpenedFile{}, false, err
	}
	return application.OpenedFile{Content: os.NewFile(uintptr(fd), position.Name), File: stored}, true, nil
}
