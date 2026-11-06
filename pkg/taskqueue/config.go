package taskqueue

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/talgent-work/workbench/internal/pkg/configvalidation"

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

// Config is the runtime configuration for the taskqueue producer, worker, and
// scheduler adapters. Populated from the fx Option tree; defaults are applied
// by the constructors, not the caller.
type Config struct {
	// RedisAddr is the Redis host:port (e.g. "redis:6379"). Required.
	RedisAddr string `yaml:"redis-addr" toml:"redis-addr" json:"redis-addr" validate:"notblank"`

	// RedisPassword is optional.
	RedisPassword string `yaml:"redis-password" toml:"redis-password" json:"redis-password"`

	// RedisDB is the logical DB index; defaults to 0.
	RedisDB int `yaml:"redis-db" toml:"redis-db" json:"redis-db"`

	// WorkerConcurrency caps the number of in-flight tasks on the worker side.
	// Defaults to 10.
	WorkerConcurrency int `yaml:"worker-concurrency" toml:"worker-concurrency" json:"worker-concurrency"`

	// QueueName is the default provider queue lane for this service.
	QueueName string `yaml:"queue-name" toml:"queue-name" json:"queue-name"`

	// QueueNames lets one merged deployable consume task definitions from
	// multiple bounded-context queues while preserving QueueName for older
	// single-service configs.
	QueueNames []string `yaml:"queue-names" toml:"queue-names" json:"queue-names"`

	// ShutdownTimeout bounds graceful drain for in-flight work. Defaults to 30s.
	ShutdownTimeout Duration `yaml:"shutdown-timeout" toml:"shutdown-timeout" json:"shutdown-timeout"`

	// RetryDelayMin and RetryDelayMax bound the delay before a failed attempt
	// is retried: it doubles from the minimum and never exceeds the maximum.
	// Both zero keeps the provider's default retry delay.
	RetryDelayMin Duration `yaml:"retry-delay-min" toml:"retry-delay-min" json:"retry-delay-min"`
	RetryDelayMax Duration `yaml:"retry-delay-max" toml:"retry-delay-max" json:"retry-delay-max"`
}

func (cfg Config) Validate() error {
	if err := configvalidation.Struct(cfg); err != nil {
		return err
	}
	low, high := cfg.RetryDelayMin.Duration(), cfg.RetryDelayMax.Duration()
	if low < 0 || high < 0 || (low == 0) != (high == 0) || low > high {
		return fmt.Errorf("taskqueue: retry delay needs 0 < retry-delay-min <= retry-delay-max, or neither")
	}
	return nil
}

// DefaultQueueName is used when a service does not configure its own queue.
const DefaultQueueName = "default"
