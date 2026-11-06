package infrastructure

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/talgent-work/workbench/internal/business/marketplace/application"
)

func (s *CatalogStore) OpenArchive(ctx context.Context, digest string) (io.ReadSeekCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	decoded, err := hex.DecodeString(digest)
	if err != nil || len(decoded) != 32 || hex.EncodeToString(decoded) != digest {
		return nil, application.ErrArchiveNotFound
	}
	root, err := os.OpenRoot(filepath.Join(s.directory, "archives"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, application.ErrArchiveNotFound
	}
	if err != nil {
		return nil, err
	}
	defer root.Close()
	file, err := root.Open(digest + ".zip")
	if errors.Is(err, fs.ErrNotExist) {
		return nil, application.ErrArchiveNotFound
	}
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		file.Close()
		return nil, application.ErrArchiveNotFound
	}
	return file, nil
}

func writeCatalogFile(filename string, contents []byte) error {
	file, err := os.CreateTemp(filepath.Dir(filename), ".catalog-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(contents); err != nil {
		file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), filename)
}
