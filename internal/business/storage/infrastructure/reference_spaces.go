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

// Reference Space layout under the JuiceFS root. A space is the directory a
// sandbox mounts: its current content is the relative symbolic link
// ".current" to one directory under ".contents", and every visible entry of
// that content appears in the space as a relative link through ".current". A
// mount resolves the links afresh on every access, so the space reads as the
// current content and follows each replacement at once. A new content is
// staged outside the space, so content never switched in is never visible,
// and content switched out is moved out of the space at once and removed
// before the replacement completes. A replacement holds the owner's lock
// exclusively and a read through Storage holds it shared, across every
// controlplane replica, so replacements never interleave and such a read sees
// one whole content.
const (
	referencesDirectory       = "assets/references"
	referenceStagingDirectory = "assets/references-staging"
	referenceRetiredDirectory = "assets/references-retired"
	referenceLocksDirectory   = "assets/references-locks"
	referenceCurrent          = ".current"
	referenceContents         = ".contents"
)

// ReferenceSpaces keeps every Reference Space on JuiceFS.
type ReferenceSpaces struct {
	root string
}

var _ application.ReferenceSpacesPort = (*ReferenceSpaces)(nil)

func NewReferenceSpaces(root string) *ReferenceSpaces { return &ReferenceSpaces{root: root} }

func referenceSpace(owner string) string {
	return path.Join(referencesDirectory, ownerComponent(owner))
}

// ownerComponent keeps an owner one visible path element. Storage admits no
// other owner; anything else maps to a name that cannot escape its parent.
func ownerComponent(owner string) string {
	if owner == "" || strings.HasPrefix(owner, ".") || strings.ContainsAny(owner, "/\\\x00") {
		return "invalid"
	}
	return owner
}

func (r *ReferenceSpaces) space(owner string) string {
	return filepath.Join(r.root, filepath.FromSlash(referenceSpace(owner)))
}

func (r *ReferenceSpaces) retired(owner string) string {
	return filepath.Join(r.root, filepath.FromSlash(path.Join(referenceRetiredDirectory, ownerComponent(owner))))
}

func (r *ReferenceSpaces) staging(owner string) string {
	return filepath.Join(r.root, filepath.FromSlash(path.Join(referenceStagingDirectory, ownerComponent(owner))))
}

func (r *ReferenceSpaces) EnsureReferenceSpace(ctx context.Context, owner string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	fd, err := openDirectory(r.root, path.Join(referenceSpace(owner), referenceContents), true, directoryMode)
	if err != nil {
		return oops.With("operation", "reference.ensure").With("owner", owner).Wrap(err)
	}
	return unix.Close(fd)
}

// current is the current content's directory name, empty before the first
// replacement.
func (r *ReferenceSpaces) current(owner string) (string, error) {
	target, err := os.Readlink(filepath.Join(r.space(owner), referenceCurrent))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return path.Base(target), nil
}

func (r *ReferenceSpaces) contentDirectory(owner, content string) string {
	return filepath.Join(r.space(owner), referenceContents, content)
}

func (r *ReferenceSpaces) lock(ctx context.Context, owner string, mode int) (func(), error) {
	return lockIn(ctx, r.root, referenceLocksDirectory, ownerComponent(owner), mode)
}

func (r *ReferenceSpaces) ReadCurrent(ctx context.Context, owner string, paths []domain.ReferencePath) ([]application.ReferenceFile, error) {
	if err := r.EnsureReferenceSpace(ctx, owner); err != nil {
		return nil, err
	}
	unlock, err := r.lock(ctx, owner, unix.LOCK_SH)
	if err != nil {
		return nil, err
	}
	defer unlock()
	content, err := r.current(owner)
	if err != nil || content == "" {
		return nil, err
	}
	directory := r.contentDirectory(owner, content)
	if len(paths) == 0 {
		paths, err = filesUnder(directory)
		if err != nil {
			return nil, oops.With("operation", "reference.read").With("owner", owner).Wrap(err)
		}
	}
	var files []application.ReferenceFile
	for _, file := range paths {
		data, err := os.ReadFile(filepath.Join(directory, filepath.FromSlash(string(file))))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, oops.With("operation", "reference.read").With("owner", owner).Wrap(err)
		}
		files = append(files, application.ReferenceFile{Path: file, Content: data})
	}
	return files, nil
}

// filesUnder lists the regular files of a content by their relative paths.
func filesUnder(directory string) ([]domain.ReferencePath, error) {
	var out []domain.ReferencePath
	err := filepath.WalkDir(directory, func(name string, entry fs.DirEntry, err error) error {
		if err != nil || !entry.Type().IsRegular() {
			return err
		}
		relative, err := filepath.Rel(directory, name)
		if err != nil {
			return err
		}
		out = append(out, domain.ReferencePath(filepath.ToSlash(relative)))
		return nil
	})
	return out, err
}

// Replace stages the files aside, moves them into the space, switches the
// space to them in one rename and removes every earlier content, holding the
// owner's lock so replacements of one owner run one at a time.
func (r *ReferenceSpaces) Replace(ctx context.Context, owner string, files []application.ReferenceFile) error {
	if err := r.EnsureReferenceSpace(ctx, owner); err != nil {
		return err
	}
	unlock, err := r.lock(ctx, owner, unix.LOCK_EX)
	if err != nil {
		return err
	}
	defer unlock()
	staging := r.staging(owner)
	// Any earlier staging was abandoned before it switched in.
	if err := os.RemoveAll(staging); err != nil {
		return oops.With("operation", "reference.stage").With("owner", owner).Wrap(err)
	}
	content := uuid.Must(uuid.NewV7()).String()
	staged := filepath.Join(staging, content)
	if err := os.MkdirAll(staged, directoryMode); err != nil {
		return oops.With("operation", "reference.stage").With("owner", owner).Wrap(err)
	}
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := writeReferenceFile(staged, file); err != nil {
			return oops.With("operation", "reference.stage").With("owner", owner).Wrap(err)
		}
	}
	switched := r.contentDirectory(owner, content)
	if err := os.Rename(staged, switched); err != nil {
		return oops.With("operation", "reference.switch").With("owner", owner).Wrap(err)
	}
	space := r.space(owner)
	// The link is made aside and renamed over ".current", so a reader finds
	// either the previous content or this one, never neither.
	if err := replaceLink(space, referenceCurrent, path.Join(referenceContents, content)); err != nil {
		return oops.With("operation", "reference.switch").With("owner", owner).Wrap(err)
	}
	if err := linkEntries(space, switched); err != nil {
		return oops.With("operation", "reference.switch").With("owner", owner).Wrap(err)
	}
	if err := syncDirectory(space); err != nil {
		return err
	}
	return r.removeEarlier(owner, content)
}

func writeReferenceFile(content string, file application.ReferenceFile) error {
	name := filepath.Join(content, filepath.FromSlash(string(file.Path)))
	if err := os.MkdirAll(filepath.Dir(name), directoryMode); err != nil {
		return err
	}
	return os.WriteFile(name, file.Content, fileMode)
}

// removeEarlier moves every content but the current one out of the space,
// beyond the reach of a mount, then removes it; a replacement completes only
// once no earlier content stays readable.
func (r *ReferenceSpaces) removeEarlier(owner, current string) error {
	contents := filepath.Join(r.space(owner), referenceContents)
	entries, err := os.ReadDir(contents)
	if err != nil {
		return oops.With("operation", "reference.retire").With("owner", owner).Wrap(err)
	}
	retired := r.retired(owner)
	for _, entry := range entries {
		if entry.Name() == current {
			continue
		}
		if err := os.MkdirAll(retired, directoryMode); err != nil {
			return oops.With("operation", "reference.retire").With("owner", owner).Wrap(err)
		}
		// Each move takes a fresh name, so one left by an earlier attempt is
		// never in the way.
		target := filepath.Join(retired, entry.Name()+"-"+uuid.Must(uuid.NewV7()).String())
		if err := os.Rename(filepath.Join(contents, entry.Name()), target); err != nil {
			return oops.With("operation", "reference.retire").With("owner", owner).Wrap(err)
		}
	}
	if err := os.RemoveAll(retired); err != nil {
		return oops.With("operation", "reference.remove_earlier").With("owner", owner).Wrap(err)
	}
	// A link left by a switch that failed before its rename is no entry.
	links, err := filepath.Glob(filepath.Join(r.space(owner), ".link-*"))
	for _, link := range links {
		_ = os.Remove(link)
	}
	return err
}

// linkEntries makes the space's visible entries the content's: each links
// through ".current", so it already shows the switched content; only an
// entry the content adds or drops is linked or unlinked here.
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
		target := path.Join(referenceCurrent, entry.Name())
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

// replaceLink points the space's entry at target in one rename.
func replaceLink(space, name, target string) error {
	link := filepath.Join(space, ".link-"+uuid.Must(uuid.NewV7()).String())
	if err := os.Symlink(target, link); err != nil {
		return err
	}
	if err := os.Rename(link, filepath.Join(space, name)); err != nil {
		_ = os.Remove(link)
		return err
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
