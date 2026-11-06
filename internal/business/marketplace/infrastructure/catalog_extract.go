package infrastructure

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

type catalogExtractor struct {
	root  *os.Root
	total int64
	count int
	names map[string]bool
	links map[string]string
}

func newCatalogExtractor(directory string) (*catalogExtractor, error) {
	root, e := os.OpenRoot(directory)
	return &catalogExtractor{root: root, names: map[string]bool{}, links: map[string]string{}}, e
}
func (x *catalogExtractor) entry(ctx context.Context, name string, mode fs.FileMode, size int64, reader io.Reader) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	name = strings.TrimSuffix(name, "/")
	if !filepath.IsLocal(name) || strings.Contains(name, "\\") || path.Clean(name) != name {
		return errors.New("marketplace: archive path escapes package")
	}
	x.count++
	if x.count > 20000 || size < 0 || size > catalogArchiveLimit-x.total {
		return errors.New("marketplace: expanded package exceeds limit")
	}
	if mode.IsDir() {
		return x.root.MkdirAll(name, 0o700)
	}
	if x.names[name] {
		return errors.New("marketplace: duplicate archive path")
	}
	x.names[name] = true
	if mode&fs.ModeSymlink != 0 {
		body, e := io.ReadAll(io.LimitReader(reader, 4097))
		if e != nil {
			return e
		}
		target := string(body)
		if len(body) > 4096 || filepath.IsAbs(target) || strings.Contains(target, "\\") || !filepath.IsLocal(filepath.Join(filepath.Dir(name), target)) {
			return errors.New("marketplace: archive symlink escapes package")
		}
		x.links[name] = target
		return nil
	}
	if !mode.IsRegular() {
		return errors.New("marketplace: unsupported archive file type")
	}
	if e := x.root.MkdirAll(filepath.Dir(name), 0o700); e != nil {
		return e
	}
	file, e := x.root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if e != nil {
		return e
	}
	n, e := io.Copy(file, io.LimitReader(reader, catalogArchiveLimit-x.total+1))
	closeErr := file.Close()
	x.total += n
	if e != nil {
		return e
	}
	if closeErr != nil {
		return closeErr
	}
	if x.total > catalogArchiveLimit || n != size {
		return errors.New("marketplace: invalid expanded package size")
	}
	return nil
}
func (x *catalogExtractor) finish() error {
	// Create links only after regular files, so an archive cannot redirect writes.
	for name, target := range x.links {
		if e := x.root.MkdirAll(filepath.Dir(name), 0o700); e != nil {
			return e
		}
		if e := x.root.Symlink(target, name); e != nil {
			return e
		}
	}
	return nil
}
func extractCatalogZIP(ctx context.Context, file, directory string) error {
	archive, e := zip.OpenReader(file)
	if e != nil {
		return errors.New("marketplace: invalid ZIP package")
	}
	defer archive.Close()
	x, e := newCatalogExtractor(directory)
	if e != nil {
		return e
	}
	defer x.root.Close()
	for _, entry := range archive.File {
		if entry.UncompressedSize64 > uint64(catalogArchiveLimit) {
			return errors.New("marketplace: expanded package exceeds limit")
		}
		reader, e := entry.Open()
		if e != nil {
			return e
		}
		e = x.entry(ctx, entry.Name, entry.Mode(), int64(entry.UncompressedSize64), reader)
		reader.Close()
		if e != nil {
			return e
		}
	}
	return x.finish()
}
func extractCatalogTarGzip(ctx context.Context, file, directory string) error {
	input, e := os.Open(file)
	if e != nil {
		return e
	}
	defer input.Close()
	compressed, e := gzip.NewReader(input)
	if e != nil {
		return e
	}
	defer compressed.Close()
	archive := tar.NewReader(compressed)
	x, e := newCatalogExtractor(directory)
	if e != nil {
		return e
	}
	defer x.root.Close()
	for {
		entry, e := archive.Next()
		if errors.Is(e, io.EOF) {
			break
		}
		if e != nil {
			return e
		}
		mode := entry.FileInfo().Mode()
		var reader io.Reader = archive
		if entry.Typeflag == tar.TypeSymlink {
			reader = strings.NewReader(entry.Linkname)
			entry.Size = int64(len(entry.Linkname))
		} else if entry.Typeflag != tar.TypeDir && entry.Typeflag != tar.TypeReg {
			return errors.New("marketplace: unsupported archive entry")
		}
		if e = x.entry(ctx, entry.Name, mode, entry.Size, reader); e != nil {
			return e
		}
	}
	return x.finish()
}

func archivePluginRoot(directory string) (string, error) {
	entries, e := os.ReadDir(directory)
	if e != nil {
		return "", e
	}
	for _, entry := range entries {
		switch entry.Name() {
		case ".claude-plugin", "skills", "agents", "commands", "hooks", "SKILL.md", ".mcp.json":
			return directory, nil
		}
	}
	if len(entries) == 1 && entries[0].IsDir() {
		return filepath.Join(directory, entries[0].Name()), nil
	}
	return directory, nil
}
