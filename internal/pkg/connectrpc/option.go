package connectrpc

import (
	"errors"
	"strings"
	"time"
)

// Option configures the process's one RPC/HTTP listener.
type Option struct {
	// Addr is the listen address, such as ":8080".
	Addr string `json:"addr" toml:"addr" yaml:"addr"`
	// ShutdownDelay keeps serving after readiness turns NOT_SERVING, so load
	// balancers stop routing before the listener closes. It counts against the
	// Fx stop timeout together with the request drain.
	ShutdownDelay time.Duration `json:"shutdown-delay" toml:"shutdown-delay" yaml:"shutdown-delay"`
}

// Validate requires a listen address and a non-negative shutdown delay.
func (o Option) Validate() error {
	if strings.TrimSpace(o.Addr) == "" {
		return errors.New("connectrpc: addr is required")
	}
	if o.ShutdownDelay < 0 {
		return errors.New("connectrpc: shutdown-delay must not be negative")
	}
	return nil
}
