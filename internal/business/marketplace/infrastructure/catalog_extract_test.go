package infrastructure

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestZIPAcquisitionRejectsUnsafeEntries(t *testing.T) {
	for _, kind := range []string{"parent-path", "absolute-path", "escaping-symlink", "oversized-entry", "duplicate-file"} {
		t.Run(kind, func(t *testing.T) {
			directory := t.TempDir()
			archive, e := os.Create(filepath.Join(directory, "package.zip"))
			require.NoError(t, e)
			writer := zip.NewWriter(archive)
			header := &zip.FileHeader{Name: "skills/check/SKILL.md", Method: zip.Store}
			body := "Review changes"
			switch kind {
			case "parent-path":
				header.Name = "../escape"
			case "absolute-path":
				header.Name = filepath.Join(directory, "escape")
			case "escaping-symlink":
				header.SetMode(os.ModeSymlink | 0777)
				body = "../../../escape"
			case "oversized-entry":
				header.UncompressedSize64 = uint64(catalogArchiveLimit + 1)
			}
			if kind == "oversized-entry" {
				_, e = writer.CreateRaw(header)
				require.NoError(t, e)
			} else {
				file, e := writer.CreateHeader(header)
				require.NoError(t, e)
				_, e = file.Write([]byte(body))
				require.NoError(t, e)
			}
			if kind == "duplicate-file" {
				file, e := writer.Create(header.Name)
				require.NoError(t, e)
				_, e = file.Write([]byte(body))
				require.NoError(t, e)
			}
			require.NoError(t, writer.Close())
			require.NoError(t, archive.Close())
			output := filepath.Join(directory, "output")
			require.NoError(t, os.Mkdir(output, 0700))
			require.Error(t, extractCatalogZIP(t.Context(), archive.Name(), output))
			require.NoFileExists(t, filepath.Join(directory, "escape"))
		})
	}
}
