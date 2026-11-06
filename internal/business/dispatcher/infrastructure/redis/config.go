package redis

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

type Duration time.Duration

func (d Duration) Duration() time.Duration {
	return time.Duration(d)
}

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		switch value.Tag {
		case "!!str":
			parsed, err := time.ParseDuration(value.Value)
			if err != nil {
				return err
			}
			*d = Duration(parsed)
			return nil
		case "!!int":
			nanos, err := strconv.ParseInt(value.Value, 10, 64)
			if err != nil {
				return err
			}
			*d = Duration(time.Duration(nanos))
			return nil
		}
	}
	return fmt.Errorf("duration must be a string or integer nanoseconds")
}

func (d *Duration) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		parsed, err := time.ParseDuration(text)
		if err != nil {
			return err
		}
		*d = Duration(parsed)
		return nil
	}
	var nanos int64
	if err := json.Unmarshal(data, &nanos); err != nil {
		return fmt.Errorf("duration must be a string or integer nanoseconds")
	}
	*d = Duration(time.Duration(nanos))
	return nil
}

// OwnershipConfig tunes replica liveness and takeover: each replica
// refreshes its liveness every Heartbeat for TTL, scans for expired replicas
// every Scan, and keeps snapshots for SnapshotTTL.
type OwnershipConfig struct {
	TTL         Duration `json:"ttl" toml:"ttl" yaml:"ttl"`
	Heartbeat   Duration `json:"heartbeat" toml:"heartbeat" yaml:"heartbeat"`
	Scan        Duration `json:"scan" toml:"scan" yaml:"scan"`
	SnapshotTTL Duration `json:"snapshot-ttl" toml:"snapshot-ttl" yaml:"snapshot-ttl"`
}

// WithDefaults fills unset durations: a 6s liveness TTL refreshed every 2s, a
// 3s scan for expired replicas and snapshots kept for 7 days.
func (c OwnershipConfig) WithDefaults() OwnershipConfig {
	if c.TTL <= 0 {
		c.TTL = Duration(6 * time.Second)
	}
	if c.Heartbeat <= 0 {
		c.Heartbeat = Duration(2 * time.Second)
	}
	if c.Scan <= 0 {
		c.Scan = Duration(3 * time.Second)
	}
	if c.SnapshotTTL <= 0 {
		c.SnapshotTTL = Duration(7 * 24 * time.Hour)
	}
	return c
}

// Validate requires a replica to stop reading a full heartbeat before its
// liveness expires, so peers never take over a stream it still reads.
func (c OwnershipConfig) Validate() error {
	if c.Heartbeat <= 0 || c.Scan <= 0 || c.SnapshotTTL <= 0 {
		return errors.New("dispatcher: ownership heartbeat, scan and snapshot TTL must be positive")
	}
	if c.TTL <= 2*c.Heartbeat {
		return errors.New("dispatcher: ownership TTL must exceed two heartbeats")
	}
	return nil
}
