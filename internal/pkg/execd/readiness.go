package execd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

const (
	// readinessInterval spaces probes of an execd that is not answering yet.
	readinessInterval = 200 * time.Millisecond
	// probeTimeout bounds one probe, so a connection that hangs does not
	// take the whole wait.
	probeTimeout = 2 * time.Second
)

// ErrNotReady reports an execd that did not answer within the wait given to
// AwaitReady.
var ErrNotReady = errors.New("execd: not ready")

// AwaitReady waits, at most for within, until execd answers its ping. execd
// starts shortly after its sandbox reports running, refusing connections until
// then, so the first call into a fresh sandbox waits for it here instead of
// failing. A connection failure or a server error is probed again; any other
// answer, such as a refused access token, ends the wait at once, since waiting
// cannot change it. The caller's own cancellation is returned as is.
func (c *Client) AwaitReady(ctx context.Context, within time.Duration) error {
	deadline := time.Now().Add(within)
	for {
		probe, cancel := context.WithTimeout(ctx, min(time.Until(deadline), probeTimeout))
		_, err := c.request(probe, http.MethodGet, c.endpoint("ping"), nil)
		cancel()
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var response *responseError
		if errors.As(err, &response) && response.status < http.StatusInternalServerError {
			return err
		}
		// No probe starts too late to be answered: the last probe's error names
		// the cause. It is not wrapped, so the wait's own deadline never reads
		// as the caller's.
		if time.Until(deadline) <= readinessInterval {
			return fmt.Errorf("%w within %s: %v", ErrNotReady, within, err)
		}
		timer := time.NewTimer(readinessInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
