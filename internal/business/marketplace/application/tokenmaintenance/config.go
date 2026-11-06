package tokenmaintenance

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	defaultScanInterval           = 5 * time.Minute
	defaultProactiveRefreshWindow = 30 * time.Minute
	defaultRequestRefreshSkew     = 5 * time.Minute
	defaultRefreshLeaseDuration   = 30 * time.Second
	defaultBatchSize              = 100
	defaultQueueName              = "scm-maintenance"

	ReasonProactivePoll  = "proactive_poll"
	ReasonAccessExpiring = "proactive_poll/access_expiring"
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

type Config struct {
	Enabled                Bool     `json:"enabled" toml:"enabled" yaml:"enabled"`
	ScanInterval           Duration `json:"scan-interval" toml:"scan-interval" yaml:"scan-interval"`
	ProactiveRefreshWindow Duration `json:"proactive-refresh-window" toml:"proactive-refresh-window" yaml:"proactive-refresh-window"`
	RequestRefreshSkew     Duration `json:"request-refresh-skew" toml:"request-refresh-skew" yaml:"request-refresh-skew"`
	RefreshLeaseDuration   Duration `json:"refresh-lease-duration" toml:"refresh-lease-duration" yaml:"refresh-lease-duration"`
	BatchSize              int      `json:"batch-size" toml:"batch-size" yaml:"batch-size"`
	QueueName              string   `json:"queue-name" toml:"queue-name" yaml:"queue-name"`
}

func (c Config) Normalize() (Config, error) {
	out := c
	if _, err := out.EnabledValue(); err != nil {
		return Config{}, err
	}
	if out.ScanInterval.Duration() <= 0 {
		out.ScanInterval = Duration(defaultScanInterval)
	}
	if out.ProactiveRefreshWindow.Duration() <= 0 {
		out.ProactiveRefreshWindow = Duration(defaultProactiveRefreshWindow)
	}
	if out.RequestRefreshSkew.Duration() <= 0 {
		out.RequestRefreshSkew = Duration(defaultRequestRefreshSkew)
	}
	if out.RefreshLeaseDuration.Duration() <= 0 {
		out.RefreshLeaseDuration = Duration(defaultRefreshLeaseDuration)
	}
	if out.BatchSize <= 0 {
		out.BatchSize = defaultBatchSize
	}
	if strings.TrimSpace(out.QueueName) == "" {
		out.QueueName = defaultQueueName
	}
	return out, nil
}

func (c Config) EnabledValue() (bool, error) {
	return c.Enabled.Bool(true)
}

type Bool struct {
	value bool
	set   bool
}

func (b Bool) Bool(defaultValue bool) (bool, error) {
	if !b.set {
		return defaultValue, nil
	}
	return b.value, nil
}

func (b *Bool) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		switch value.Tag {
		case "!!bool":
			parsed, err := strconv.ParseBool(value.Value)
			if err != nil {
				return err
			}
			b.value = parsed
			b.set = true
			return nil
		case "!!str":
			return b.setFromString(value.Value)
		}
	}
	return fmt.Errorf("bool must be a boolean or string")
}

func (b *Bool) UnmarshalJSON(data []byte) error {
	var parsed bool
	if err := json.Unmarshal(data, &parsed); err == nil {
		b.value = parsed
		b.set = true
		return nil
	}
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return fmt.Errorf("bool must be a boolean or string")
	}
	return b.setFromString(text)
}

func (b *Bool) setFromString(value string) error {
	parsed, err := strconv.ParseBool(strings.TrimSpace(value))
	if err != nil {
		return fmt.Errorf("scm maintenance enabled must be boolean: %w", err)
	}
	b.value = parsed
	b.set = true
	return nil
}
