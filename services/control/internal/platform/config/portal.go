package config

import (
	"errors"
	"go.yaml.in/yaml/v3"
	"reflect"
)

type PortalPolicy struct {
	CommitSeconds                  int `yaml:"commit_seconds"`
	NotificationRetentionDays      int `yaml:"notification_retention_days"`
	NotificationMaintenanceBatch   int `yaml:"notification_maintenance_batch"`
	NotificationMaintenanceSeconds int `yaml:"notification_maintenance_seconds"`
}

func DefaultPortalPolicy() PortalPolicy {
	return PortalPolicy{CommitSeconds: 10, NotificationRetentionDays: 30, NotificationMaintenanceBatch: 100, NotificationMaintenanceSeconds: 60}
}
func (p PortalPolicy) Validate() error {
	if p.CommitSeconds < 1 || p.CommitSeconds > 30 || p.NotificationRetentionDays < 1 || p.NotificationRetentionDays > 365 || p.NotificationMaintenanceBatch < 1 || p.NotificationMaintenanceBatch > 1000 || p.NotificationMaintenanceSeconds < 1 || p.NotificationMaintenanceSeconds > 3600 {
		return errors.New("CONFIG_PORTAL_POLICY_INVALID")
	}
	return nil
}
func (p *PortalPolicy) UnmarshalYAML(node *yaml.Node) error {
	if strictKnowledgeYAML(node, reflect.TypeOf(*p)) != nil {
		return errors.New("CONFIG_PORTAL_POLICY_INVALID")
	}
	type plain PortalPolicy
	value := plain(*p)
	if node.Decode(&value) != nil {
		return errors.New("CONFIG_PORTAL_POLICY_INVALID")
	}
	*p = PortalPolicy(value)
	return nil
}
func validatePortalYAML(raw []byte) error {
	var document yaml.Node
	if yaml.Unmarshal(raw, &document) != nil || len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return errors.New("CONFIG_PORTAL_POLICY_INVALID")
	}
	root := document.Content[0]
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "runtime" && root.Content[i+1].Kind == yaml.MappingNode {
			runtime := root.Content[i+1]
			for j := 0; j+1 < len(runtime.Content); j += 2 {
				if runtime.Content[j].Value == "portal" && strictKnowledgeYAML(runtime.Content[j+1], reflect.TypeOf(PortalPolicy{})) != nil {
					return errors.New("CONFIG_PORTAL_POLICY_INVALID")
				}
			}
		}
	}
	return nil
}
