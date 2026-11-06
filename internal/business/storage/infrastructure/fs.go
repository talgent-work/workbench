package infrastructure

import (
	"errors"
	"os"
	"path"
	"sort"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/talgent-work/workbench/internal/business/storage/application"
	"github.com/talgent-work/workbench/pkg/storagepath"
)

// openDirectory opens a directory relative to root without following any
// symbolic link, creating missing components with mode when create is set.
func openDirectory(root, relative string, create bool, mode uint32) (int, error) {
	if strings.TrimSpace(root) == "" {
		return -1, errors.New("storage: root is required")
	}
	fd, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	clean := path.Clean(relative)
	if clean == "." {
		return fd, nil
	}
	if path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
		unix.Close(fd)
		return -1, application.ErrInvalidWorkspacePath
	}
	for _, part := range strings.Split(clean, "/") {
		next, err := openChildDirectory(fd, part, create, mode)
		unix.Close(fd)
		if err != nil {
			return -1, err
		}
		fd = next
	}
	return fd, nil
}

func openChildDirectory(parent int, name string, create bool, mode uint32) (int, error) {
	const flags = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC
	fd, err := unix.Openat(parent, name, flags, 0)
	if errors.Is(err, unix.ENOENT) && create {
		if mkdirErr := unix.Mkdirat(parent, name, mode); mkdirErr != nil && !errors.Is(mkdirErr, unix.EEXIST) {
			return -1, mkdirErr
		}
		fd, err = unix.Openat(parent, name, flags, 0)
	}
	return fd, err
}

// attributeDigest is the content's SHA-256, Storage's own and unpublished.
const attributeDigest = "user.talgent.digest"

var attributeKeys = []string{storagepath.AttributeUploader, storagepath.AttributeWork, storagepath.AttributeContentType, attributeDigest}

// maxAttributeBytes bounds one attribute value, so every value written is
// read back whole.
const maxAttributeBytes = 512

func writeAttributes(fd int, attributes application.FileAttributes) error {
	for key, value := range map[string]string{
		storagepath.AttributeUploader:    attributes.UploaderUserID,
		storagepath.AttributeWork:        attributes.WorkID,
		storagepath.AttributeContentType: attributes.ContentType,
		attributeDigest:                  attributes.ContentDigest,
	} {
		if value == "" {
			continue
		}
		if len(value) > maxAttributeBytes {
			return unix.ERANGE
		}
		if err := unix.Fsetxattr(fd, key, []byte(value), 0); err != nil {
			return err
		}
	}
	return nil
}

func readAttributes(fd int) (application.FileAttributes, error) {
	values := make(map[string]string, len(attributeKeys))
	buf := make([]byte, maxAttributeBytes)
	for _, key := range attributeKeys {
		n, err := unix.Fgetxattr(fd, key, buf)
		switch {
		case err == nil:
			values[key] = string(buf[:n])
		case errors.Is(err, unix.ENODATA):
			// An empty value is never written.
		default:
			return application.FileAttributes{}, err
		}
	}
	return application.FileAttributes{
		UploaderUserID: values[storagepath.AttributeUploader],
		WorkID:         values[storagepath.AttributeWork],
		ContentType:    values[storagepath.AttributeContentType],
		ContentDigest:  values[attributeDigest],
	}, nil
}

// directoryNames lists an open directory, sorted, leaving fd open.
func directoryNames(fd int) ([]string, error) {
	dup, err := unix.Dup(fd)
	if err != nil {
		return nil, err
	}
	dir := os.NewFile(uintptr(dup), "")
	defer dir.Close()
	names, err := dir.Readdirnames(-1)
	sort.Strings(names)
	return names, err
}
