package engine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestDatabaseTaskLocalizationMountAndForeignConsumerFences(t *testing.T) {
	for _, fault := range []string{"locale-positive", "locale-missing", "locale-foreign-consumer", "locale-request-rw", "locale-actual-rw", "locale-nocopy", "locale-target"} {
		t.Run(fault, func(t *testing.T) {
			c, task, _, _, _ := databaseTaskFixture(t, fault, ModelFilesExport)
			_, err := c.CreateDatabaseTask(context.Background(), task)
			if (err == nil) != (fault == "locale-positive") {
				t.Fatal(fault, err)
			}
		})
	}
}
func TestDatabaseTaskLocalizationOwnerAndLegacyEncoding(t *testing.T) {
	_, task, _, _, _ := databaseTaskFixture(t, "none", ModelFilesExport)
	raw, _ := json.Marshal(task)
	if strings.Contains(string(raw), "localization_owner") {
		t.Fatal("legacy task encoding changed")
	}
	for _, fault := range []string{"valid", "scope", "role", "operation", "control"} {
		t.Run(fault, func(t *testing.T) {
			v := task
			owner := Ownership{task.Owner.ScopeID, strings.Repeat("2", 32), LocalizationConfigurationRole}
			v.LocalizationOwner = &owner
			switch fault {
			case "scope":
				owner.ScopeID = strings.Repeat("3", 32)
			case "role":
				owner.Role = "operator-config"
			case "operation":
				owner.OperationID = "unknown"
			case "control":
				v.Peers = map[string]string{"postgres": task.Peers["postgres"]}
			}
			if v.valid() != (fault == "valid") {
				t.Fatal("invalid locale mount authority", fault)
			}
		})
	}
}
