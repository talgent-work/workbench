package execd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"strconv"
	"strings"

	"github.com/samber/oops"
)

// fileMetadata is execd's upload metadata: the target path and its mode as
// octal digits written in decimal, such as 600 for rw-------.
type fileMetadata struct {
	Path string `json:"path"`
	Mode int    `json:"mode"`
}

// Upload writes content to an absolute path in the environment with mode,
// replacing any file there, through execd's file API. Neither its errors
// nor anything it logs carry the content or execd's response body.
func (c *Client) Upload(ctx context.Context, path string, mode os.FileMode, content []byte) error {
	if !strings.HasPrefix(path, "/") || strings.ContainsRune(path, 0) || mode&^os.ModePerm != 0 {
		return errors.New("execd: absolute file path and permission bits required")
	}
	octal, err := strconv.Atoi(strconv.FormatUint(uint64(mode.Perm()), 8))
	if err != nil {
		return err
	}
	metadata, err := json.Marshal(fileMetadata{Path: path, Mode: octal})
	if err != nil {
		return err
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", `form-data; name="metadata"; filename="metadata"`)
	header.Set("Content-Type", "application/json")
	part, err := writer.CreatePart(header)
	if err == nil {
		_, err = part.Write(metadata)
	}
	if err == nil {
		part, err = writer.CreateFormFile("file", "file")
	}
	if err == nil {
		_, err = part.Write(content)
	}
	if err == nil {
		err = writer.Close()
	}
	if err != nil {
		return oops.With("operation", "upload").Wrap(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint("files", "upload").String(), &body)
	if err != nil {
		return oops.With("operation", "upload").Wrap(err)
	}
	req.Header.Set(accessHeader, c.token)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := c.http.Do(req)
	if err != nil {
		return oops.With("operation", "upload").Wrap(err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &responseError{method: fmt.Sprintf("%s /files/upload", http.MethodPost), status: resp.StatusCode}
	}
	return nil
}
