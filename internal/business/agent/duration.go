package agent

import (
	"encoding/json"
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
