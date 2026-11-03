package mysqlmigrate

import (
	"bytes"
	"strings"
	"testing"

	assets "github.com/Passion-Flow/ownsolid/services/control/migrations/mysql"
)

func TestMySQLModelInferenceSchemaImmutablePrefixAndOwnership(t *testing.T) {
	c, e := DefaultCatalog()
	if e != nil || c.Steps() < 51 {
		t.Fatal("current base model/inference catalog", e)
	}
	raw, _ := assets.Files.ReadFile("evolution.json")
	cut := bytes.LastIndex(raw, []byte(",\n    {\n      \"step\": 46,"))
	if cut < 0 || hash(string(append(append([]byte{}, raw[:cut]...), []byte("\n  ]\n}\n")...))) != "0a787732ba628f17af7328794b36dfd1ed73adcb609f17caf45f3bfd6a5b5f21" {
		t.Fatal("immutable1-45 serialized evolution prefix changed")
	}
	for n, name := range []string{"model_versions", "model_imports", "model_import_chunks", "inference_nodes", "deployments", "gpu_leases"} {
		entry := c.steps[45+n]
		if entry.Step != 46+n || entry.Object != name || entry.Kind != createTable {
			t.Fatal("base model/inference dependency ownership changed")
		}
	}
}

func TestMySQLModelInferenceSchemaGeneratedAndScopeContracts(t *testing.T) {
	c, e := DefaultCatalog()
	if e != nil {
		t.Fatal(e)
	}
	for index, tokens := range map[int][]string{
		45: {"`ready_catalog_digest` varchar(64)", "STORED", "UNIQUE KEY `model_ready_catalog_idx`", "UNIQUE KEY `model_scope_identity`", "`manifest` json NOT NULL", "qwen3-0.6b"},
		46: {"`active_workspace_id` char(36)", "STORED", "UNIQUE KEY `model_import_active_workspace_idx`", "`fingerprint` varbinary(32) NOT NULL", "`expected_bytes` bigint NOT NULL", "FOREIGN KEY (`tenant_id`, `workspace_id`, `model_id`) REFERENCES `model_versions`"},
		47: {"PRIMARY KEY (`import_id`,`byte_offset`)", "8388608", "FOREIGN KEY (`import_id`) REFERENCES `model_imports`"},
		48: {"`observation` json NOT NULL", "32768", "FOREIGN KEY (`tenant_id`, `workspace_id`, `node_id`) REFERENCES `managed_nodes`"},
		49: {"`retained_node_id` char(36)", "UNIQUE KEY `deployment_one_retained_node_idx`", "UNIQUE KEY `deployment_node_identity`", "FOREIGN KEY (`tenant_id`, `workspace_id`, `node_id`, `last_task_id`) REFERENCES `node_tasks`", "`recovery_receipt_sha256` varchar(64)"},
		50: {"PRIMARY KEY (`deployment_id`)", "`reserved_node_id` char(36)", "`reserved_gpu_uuid` varchar(64)", "UNIQUE KEY `gpu_exclusive_node_idx`", "UNIQUE KEY `gpu_exclusive_uuid_idx`", "recovery_required", "FOREIGN KEY (`tenant_id`, `workspace_id`, `node_id`, `deployment_id`) REFERENCES `deployments`"},
	} {
		for _, token := range tokens {
			if !strings.Contains(c.steps[index].Definition, token) {
				t.Fatalf("measured base model/inference definition lost required token %q", token)
			}
		}
	}
}
