package execd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
)

// Terminate lets a running command clean up its child processes before deleting
// its handle. DELETE alone force-kills the process group and bypasses cleanup
// handlers. A deadline detaches without deleting; callers retain the handle for
// recovery. An unstarted reservation is deleted without launching its command.
// The caller must serialize it with other operations that can attach this handle.
func (c *Client) Terminate(ctx context.Context, id string) error {
	if !validID(id) {
		return errors.New("execd: invalid process handle")
	}
	body, err := c.request(ctx, http.MethodGet, c.endpoint("pty", id), nil)
	var response *responseError
	if errors.As(err, &response) && response.status == http.StatusNotFound {
		return nil
	}
	if err != nil {
		return err
	}
	var status struct {
		SessionID string `json:"session_id"`
		Running   *bool  `json:"running"`
	}
	if json.Unmarshal(body, &status) != nil || status.SessionID != id || status.Running == nil {
		return errors.New("execd: invalid process status")
	}
	if *status.Running {
		if err := c.terminateRunning(ctx, id); err != nil {
			return err
		}
	}
	return c.Delete(ctx, id)
}

func (c *Client) terminateRunning(ctx context.Context, id string) error {
	pipe, err := c.Attach(ctx, id, 0)
	if err != nil {
		return err
	}
	defer pipe.Close()
	for {
		frame, err := pipe.Receive(ctx)
		if err != nil {
			return err
		}
		switch frame.Type {
		case "connected":
			if err := pipe.Signal(ctx, "SIGTERM"); err != nil {
				return err
			}
		case "exit":
			return nil
		}
	}
}
