package infrastructure

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path"
	"time"

	"github.com/google/uuid"
	"github.com/samber/oops"
	"golang.org/x/sys/unix"

	"github.com/talgent-work/workbench/internal/business/storage/application"
	"github.com/talgent-work/workbench/internal/business/storage/domain"
	"github.com/talgent-work/workbench/pkg/storagepath"
)

// Persistent storage layout under the JuiceFS root. Only
// spacesDirectory is published, as the originals mount.
const (
	spacesDirectory  = "assets/spaces"
	pendingDirectory = "assets/pending"
	fileMode         = 0o644
	directoryMode    = 0o755
	// stalePendingAge is how long staged content may wait for its commit; an
	// older entry was left by a process that ended mid-write.
	stalePendingAge = time.Hour
)

// PersistentFiles keeps File Spaces on JuiceFS, laid out by owner. Staged content becomes
// visible by one atomic rename, so readers never see a partial file; Storage
// is the only writer, readers mount it read-only.
type PersistentFiles struct {
	root string
	now  func() time.Time
}

var _ application.PersistentFilesPort = (*PersistentFiles)(nil)

func NewPersistentFiles(root string) *PersistentFiles {
	return &PersistentFiles{root: root, now: time.Now}
}

func kindDirectory(owner string, kind domain.FileKind) string {
	if kind == domain.FileKindArtifact {
		return path.Join(spacesDirectory, storagepath.Artifacts(owner))
	}
	return path.Join(spacesDirectory, storagepath.Attachments(owner))
}

func (p *PersistentFiles) EnsureSpace(ctx context.Context, owner string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, kind := range []domain.FileKind{domain.FileKindAttachment, domain.FileKindArtifact} {
		fd, err := openDirectory(p.root, kindDirectory(owner, kind), true, directoryMode)
		if err != nil {
			return oops.With("operation", "storage.ensure_space", "owner", owner).Wrap(err)
		}
		unix.Close(fd)
	}
	return nil
}

func (p *PersistentFiles) List(ctx context.Context, owner string) ([]application.StoredFile, error) {
	var files []application.StoredFile
	for _, kind := range []domain.FileKind{domain.FileKindAttachment, domain.FileKindArtifact} {
		listed, err := p.listKind(ctx, owner, kind)
		if err != nil {
			return nil, err
		}
		files = append(files, listed...)
	}
	return files, nil
}

// listKind lists the files of one kind; an entry removed meanwhile, or one
// that is not a file name, is not a file of the space.
func (p *PersistentFiles) listKind(ctx context.Context, owner string, kind domain.FileKind) ([]application.StoredFile, error) {
	fd, err := openDirectory(p.root, kindDirectory(owner, kind), false, 0)
	if errors.Is(err, unix.ENOENT) {
		return nil, nil
	}
	if err != nil {
		return nil, oops.With("operation", "storage.list", "owner", owner).Wrap(err)
	}
	defer unix.Close(fd)
	names, err := directoryNames(fd)
	if err != nil {
		return nil, oops.With("operation", "storage.list", "owner", owner).Wrap(err)
	}
	var files []application.StoredFile
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		position, err := domain.NewFilePosition(owner, kind, name)
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

func (p *PersistentFiles) Stat(ctx context.Context, owner string, position domain.FilePosition) (application.StoredFile, error) {
	if err := ctx.Err(); err != nil {
		return application.StoredFile{}, err
	}
	fd, err := openDirectory(p.root, kindDirectory(owner, position.Kind), false, 0)
	if errors.Is(err, unix.ENOENT) {
		return application.StoredFile{}, application.ErrFileNotFound
	}
	if err != nil {
		return application.StoredFile{}, oops.With("operation", "storage.stat", "owner", owner).Wrap(err)
	}
	defer unix.Close(fd)
	return statAt(fd, position)
}

// stagedContent is complete content waiting outside every space's
// directory for its commit.
type stagedContent struct {
	Name string
}

// Write stages the content, then commits it; staged content that cannot be
// committed is discarded, and the pending sweep removes what a crash leaves.
func (p *PersistentFiles) Write(ctx context.Context, owner string, position domain.FilePosition, content io.Reader, attributes application.FileAttributes, replace bool) (application.StoredFile, error) {
	staged, err := p.stage(ctx, content, attributes)
	if err != nil {
		return application.StoredFile{}, err
	}
	stored, err := p.commit(ctx, staged, owner, position, replace)
	if err != nil {
		p.discard(staged)
		return application.StoredFile{}, err
	}
	return stored, nil
}

func (p *PersistentFiles) stage(ctx context.Context, content io.Reader, attributes application.FileAttributes) (stagedContent, error) {
	pending, err := openDirectory(p.root, pendingDirectory, true, directoryMode)
	if err != nil {
		return stagedContent{}, oops.With("operation", "storage.stage").Wrap(err)
	}
	defer unix.Close(pending)
	p.sweepPending(pending)
	name := uuid.Must(uuid.NewV7()).String()
	fd, err := unix.Openat(pending, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, fileMode)
	if err != nil {
		return stagedContent{}, oops.With("operation", "storage.stage").Wrap(err)
	}
	file := os.NewFile(uintptr(fd), name)
	staged := stagedContent{Name: name}
	if err := writeStaged(ctx, file, content, attributes); err != nil {
		file.Close()
		_ = unix.Unlinkat(pending, name, 0)
		return stagedContent{}, err
	}
	if err := file.Close(); err != nil {
		_ = unix.Unlinkat(pending, name, 0)
		return stagedContent{}, oops.With("operation", "storage.stage").Wrap(err)
	}
	return staged, nil
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

func (p *PersistentFiles) commit(ctx context.Context, staged stagedContent, owner string, position domain.FilePosition, replace bool) (application.StoredFile, error) {
	if err := ctx.Err(); err != nil {
		return application.StoredFile{}, err
	}
	pending, err := openDirectory(p.root, pendingDirectory, false, 0)
	if err != nil {
		return application.StoredFile{}, oops.With("operation", "storage.commit").Wrap(err)
	}
	defer unix.Close(pending)
	target, err := openDirectory(p.root, kindDirectory(owner, position.Kind), true, directoryMode)
	if err != nil {
		return application.StoredFile{}, oops.With("operation", "storage.commit", "owner", owner).Wrap(err)
	}
	defer unix.Close(target)
	if replace {
		// The exchange fails on an absent target, and the previous content stays
		// in place until the new one has taken its position.
		err = unix.Renameat2(pending, staged.Name, target, position.Name, unix.RENAME_EXCHANGE)
		if errors.Is(err, unix.ENOENT) {
			return application.StoredFile{}, application.ErrFileNotFound
		}
		if err != nil {
			return application.StoredFile{}, oops.With("operation", "storage.replace", "owner", owner).Wrap(err)
		}
		_ = unix.Unlinkat(pending, staged.Name, 0)
	} else {
		err = unix.Renameat2(pending, staged.Name, target, position.Name, unix.RENAME_NOREPLACE)
		if errors.Is(err, unix.EEXIST) {
			return application.StoredFile{}, application.ErrNameTaken
		}
		if err != nil {
			return application.StoredFile{}, oops.With("operation", "storage.create", "owner", owner).Wrap(err)
		}
	}
	_ = unix.Fsync(target)
	return statAt(target, position)
}

// discard removes staged content at once; should that fail, the pending
// sweep removes it later.
func (p *PersistentFiles) discard(staged stagedContent) {
	pending, err := openDirectory(p.root, pendingDirectory, false, 0)
	if err != nil {
		return
	}
	defer unix.Close(pending)
	_ = unix.Unlinkat(pending, staged.Name, 0)
}

func (p *PersistentFiles) Rename(ctx context.Context, owner string, from, to domain.FilePosition) (application.StoredFile, error) {
	if err := ctx.Err(); err != nil {
		return application.StoredFile{}, err
	}
	fd, err := openDirectory(p.root, kindDirectory(owner, from.Kind), false, 0)
	if errors.Is(err, unix.ENOENT) {
		return application.StoredFile{}, application.ErrFileNotFound
	}
	if err != nil {
		return application.StoredFile{}, oops.With("operation", "storage.rename", "owner", owner).Wrap(err)
	}
	defer unix.Close(fd)
	err = unix.Renameat2(fd, from.Name, fd, to.Name, unix.RENAME_NOREPLACE)
	switch {
	case errors.Is(err, unix.ENOENT):
		return application.StoredFile{}, application.ErrFileNotFound
	case errors.Is(err, unix.EEXIST):
		return application.StoredFile{}, application.ErrNameTaken
	case err != nil:
		return application.StoredFile{}, oops.With("operation", "storage.rename", "owner", owner).Wrap(err)
	}
	_ = unix.Fsync(fd)
	return statAt(fd, to)
}

func (p *PersistentFiles) Remove(ctx context.Context, owner string, position domain.FilePosition) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	fd, err := openDirectory(p.root, kindDirectory(owner, position.Kind), false, 0)
	if errors.Is(err, unix.ENOENT) {
		return false, nil
	}
	if err != nil {
		return false, oops.With("operation", "storage.remove", "owner", owner).Wrap(err)
	}
	defer unix.Close(fd)
	// Of concurrent deletions only the one whose unlink succeeds removed it.
	err = unix.Unlinkat(fd, position.Name, 0)
	if errors.Is(err, unix.ENOENT) {
		return false, nil
	}
	if err != nil {
		return false, oops.With("operation", "storage.remove", "owner", owner).Wrap(err)
	}
	return true, nil
}

func (p *PersistentFiles) Open(ctx context.Context, owner string, position domain.FilePosition) (application.OpenedFile, error) {
	if err := ctx.Err(); err != nil {
		return application.OpenedFile{}, err
	}
	dir, err := openDirectory(p.root, kindDirectory(owner, position.Kind), false, 0)
	if errors.Is(err, unix.ENOENT) {
		return application.OpenedFile{}, application.ErrFileNotFound
	}
	if err != nil {
		return application.OpenedFile{}, oops.With("operation", "storage.open", "owner", owner).Wrap(err)
	}
	defer unix.Close(dir)
	fd, err := unix.Openat(dir, position.Name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if errors.Is(err, unix.ENOENT) || errors.Is(err, unix.ELOOP) {
		return application.OpenedFile{}, application.ErrFileNotFound
	}
	if err != nil {
		return application.OpenedFile{}, oops.With("operation", "storage.open", "owner", owner).Wrap(err)
	}
	stored, err := describe(fd, position)
	if err != nil {
		unix.Close(fd)
		return application.OpenedFile{}, err
	}
	return application.OpenedFile{Content: os.NewFile(uintptr(fd), position.Name), File: stored}, nil
}

// sweepPending removes staged content older than stalePendingAge. It is best
// effort: whatever it cannot remove now, a later write sweeps again.
func (p *PersistentFiles) sweepPending(pending int) {
	names, err := directoryNames(pending)
	if err != nil {
		return
	}
	now := p.now()
	for _, name := range names {
		var stat unix.Stat_t
		if unix.Fstatat(pending, name, &stat, unix.AT_SYMLINK_NOFOLLOW) != nil {
			continue
		}
		if now.Sub(time.Unix(stat.Mtim.Unix())) >= stalePendingAge {
			_ = unix.Unlinkat(pending, name, 0)
		}
	}
}

// statAt describes the regular file at a position of an open kind directory.
func statAt(directory int, position domain.FilePosition) (application.StoredFile, error) {
	fd, err := unix.Openat(directory, position.Name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if errors.Is(err, unix.ENOENT) || errors.Is(err, unix.ELOOP) {
		return application.StoredFile{}, application.ErrFileNotFound
	}
	if err != nil {
		return application.StoredFile{}, oops.With("operation", "storage.stat", "owner", position.Owner).Wrap(err)
	}
	defer unix.Close(fd)
	return describe(fd, position)
}

func describe(fd int, position domain.FilePosition) (application.StoredFile, error) {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return application.StoredFile{}, oops.With("operation", "storage.stat", "owner", position.Owner).Wrap(err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG {
		return application.StoredFile{}, application.ErrFileNotFound
	}
	attributes, err := readAttributes(fd)
	if err != nil {
		return application.StoredFile{}, oops.With("operation", "storage.read_attributes", "owner", position.Owner).Wrap(err)
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

// Inspect reads the facts Preview Policy judges from the file's current
// content, together with the file's own facts from the same open file.
func (p *PersistentFiles) Inspect(ctx context.Context, owner string, position domain.FilePosition) (application.StoredFile, domain.ContentStructure, error) {
	opened, err := p.Open(ctx, owner, position)
	if err != nil {
		return application.StoredFile{}, domain.ContentStructure{}, err
	}
	defer opened.Content.Close()
	reader, ok := opened.Content.(io.ReaderAt)
	if !ok {
		return application.StoredFile{}, domain.ContentStructure{}, oops.Errorf("storage: persistent content is not randomly readable")
	}
	structure, err := inspectContent(reader, opened.File.SizeBytes)
	if err != nil {
		return application.StoredFile{}, domain.ContentStructure{}, oops.With("operation", "storage.inspect", "owner", owner).Wrap(err)
	}
	return opened.File, structure, nil
}
