package config

import (
	"errors"
	"strings"

	"go.yaml.in/yaml/v3"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/taskbus"
)

func taskEventEnvironment(key string) bool {
	return strings.HasPrefix(key, taskbus.EnvironmentPrefix)
}

func validateTaskEventsYAML(raw []byte) error {
	var document yaml.Node
	if yaml.Unmarshal(raw, &document) != nil || len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return errors.New("CONFIG_TASK_EVENTS_INVALID")
	}
	root := document.Content[0]
	for n := 0; n < len(root.Content); n += 2 {
		if root.Content[n].Value == "task_events" {
			if e := taskbus.ValidateYAMLNode(root.Content[n+1]); e != nil {
				return e
			}
		}
	}
	return nil
}
