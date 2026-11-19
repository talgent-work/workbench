package domain

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
)

// MaxConnectorIconBytes bounds an icon image.
const MaxConnectorIconBytes = 512 * 1024

var ErrInvalidConnectorIcon = errors.New("marketplace: a Connector icon is a PNG, SVG, JPEG or WebP image of at most 512 KiB")

// ConnectorIcon is a Connector's optional icon image, part of its catalog
// presentation.
type ConnectorIcon struct {
	MediaType string
	Data      []byte
}

// Validate admits a PNG, SVG, JPEG or WebP image of at most 512 KiB whose
// content is of its declared type.
func (i ConnectorIcon) Validate() error {
	if len(i.Data) == 0 || len(i.Data) > MaxConnectorIconBytes {
		return ErrInvalidConnectorIcon
	}
	var valid bool
	switch i.MediaType {
	case "image/png":
		valid = bytes.HasPrefix(i.Data, []byte("\x89PNG\r\n\x1a\n"))
	case "image/jpeg":
		valid = bytes.HasPrefix(i.Data, []byte{0xff, 0xd8, 0xff})
	case "image/webp":
		valid = len(i.Data) > 12 && bytes.Equal(i.Data[:4], []byte("RIFF")) && bytes.Equal(i.Data[8:12], []byte("WEBP"))
	case "image/svg+xml":
		// Shown only as an image, where an SVG runs no script.
		valid = bytes.Contains(bytes.ToLower(i.Data[:min(len(i.Data), 4096)]), []byte("<svg"))
	}
	if !valid {
		return ErrInvalidConnectorIcon
	}
	return nil
}

// Digest identifies the image, so a reader caches it until it changes.
func (i ConnectorIcon) Digest() string {
	sum := sha256.Sum256(append([]byte(i.MediaType+"\x00"), i.Data...))
	return hex.EncodeToString(sum[:16])
}
