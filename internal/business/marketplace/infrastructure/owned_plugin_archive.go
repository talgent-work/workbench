package infrastructure

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Owned packages use the configured persistent content volume. Publication
// produces deterministic archives addressed by their digest; third-party
// packages are never copied into this store.
func (s *CatalogSynchronizer) publishOwnedPackage(ctx context.Context, directory string) (map[string]any, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	archiveDirectory := filepath.Join(s.store.directory, "archives")
	if err := os.MkdirAll(archiveDirectory, 0o700); err != nil {
		return nil, err
	}
	file, err := os.CreateTemp(archiveDirectory, ".publish-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	hash := sha256.New()
	writer := zip.NewWriter(io.MultiWriter(file, hash))
	var total int64
	count := 0
	err = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		count++
		if count > 20000 {
			return errors.New("marketplace: owned package contains too many files")
		}
		input, err := root.Open(name)
		if err != nil {
			return err
		}
		defer input.Close()
		info, err := input.Stat()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("marketplace: owned package contains a non-file entry")
		}
		header := &zip.FileHeader{Name: filepath.ToSlash(name), Method: zip.Deflate}
		mode := fs.FileMode(0o644)
		if info.Mode()&0o111 != 0 {
			mode = 0o755
		}
		header.SetMode(mode)
		output, err := writer.CreateHeader(header)
		if err != nil {
			return err
		}
		written, err := io.Copy(output, io.LimitReader(input, (256<<20)-total+1))
		total += written
		if err != nil {
			return err
		}
		if total > 256<<20 {
			return errors.New("marketplace: owned package exceeds size limit")
		}
		return nil
	})
	if err != nil {
		writer.Close()
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	if err := file.Sync(); err != nil {
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	if err := os.Rename(file.Name(), filepath.Join(archiveDirectory, digest+".zip")); err != nil {
		return nil, err
	}
	return map[string]any{"source": "archive", "url": strings.TrimSuffix(s.config.ArchiveBaseURL, "/") + "/" + digest + ".zip", "sha256": digest}, nil
}
