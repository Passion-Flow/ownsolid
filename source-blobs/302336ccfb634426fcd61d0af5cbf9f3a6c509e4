package config

import (
	"go.yaml.in/yaml/v3"
	"testing"
)

func TestPortalPolicyDefaultsPartialOverridesStrictKeysAndBounds(t *testing.T) {
	p := DefaultPortalPolicy()
	if p.Validate() != nil {
		t.Fatal("default portal policy")
	}
	if e := yaml.Unmarshal([]byte("commit_seconds: 7\nnotification_retention_days: 14\nnotification_maintenance_batch: 25\nnotification_maintenance_seconds: 45\n"), &p); e != nil || p.CommitSeconds != 7 || p.NotificationRetentionDays != 14 || p.NotificationMaintenanceBatch != 25 || p.NotificationMaintenanceSeconds != 45 || p.Validate() != nil {
		t.Fatal("nondefault portal policy", e)
	}
	for name, change := range map[string]func(*PortalPolicy){"commit_zero": func(p *PortalPolicy) { p.CommitSeconds = 0 }, "commit_excess": func(p *PortalPolicy) { p.CommitSeconds = 31 }, "retention_zero": func(p *PortalPolicy) { p.NotificationRetentionDays = 0 }, "retention_excess": func(p *PortalPolicy) { p.NotificationRetentionDays = 366 }, "batch_zero": func(p *PortalPolicy) { p.NotificationMaintenanceBatch = 0 }, "batch_excess": func(p *PortalPolicy) { p.NotificationMaintenanceBatch = 1001 }, "interval_zero": func(p *PortalPolicy) { p.NotificationMaintenanceSeconds = 0 }, "interval_excess": func(p *PortalPolicy) { p.NotificationMaintenanceSeconds = 3601 }} {
		t.Run(name, func(t *testing.T) {
			p := DefaultPortalPolicy()
			change(&p)
			if p.Validate() == nil {
				t.Fatal("invalid policy accepted")
			}
		})
	}
	for _, raw := range []string{"runtime:\n  portal: null\n", "runtime:\n  portal: []\n", "runtime:\n  portal:\n    unknown: 1\n", "runtime:\n  portal:\n    Commit_seconds: 1\n", "runtime:\n  portal:\n    commit_seconds: null\n", "runtime:\n  portal:\n    commit_seconds: 1\n    commit_seconds: 2\n"} {
		if validatePortalYAML([]byte(raw)) == nil {
			t.Fatal("ambiguous portal YAML accepted")
		}
	}
}
