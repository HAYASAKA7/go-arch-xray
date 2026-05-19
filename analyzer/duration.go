package analyzer

import (
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

type ConfigDuration time.Duration

func (d ConfigDuration) Duration() time.Duration {
	return time.Duration(d)
}

func (d ConfigDuration) MarshalYAML() (any, error) {
	if d == 0 {
		return "", nil
	}
	return time.Duration(d).String(), nil
}

func (d *ConfigDuration) UnmarshalYAML(node *yaml.Node) error {
	if node == nil {
		return nil
	}
	switch node.Tag {
	case "!!str":
		parsed, err := time.ParseDuration(node.Value)
		if err != nil {
			return err
		}
		*d = ConfigDuration(parsed)
		return nil
	case "!!int":
		var value int64
		if err := node.Decode(&value); err != nil {
			return err
		}
		*d = ConfigDuration(time.Duration(value))
		return nil
	case "!!float":
		var value float64
		if err := node.Decode(&value); err != nil {
			return err
		}
		*d = ConfigDuration(time.Duration(value))
		return nil
	default:
		return fmt.Errorf("unsupported duration value %q", node.Value)
	}
}
