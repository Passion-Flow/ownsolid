package mysqlmigrate

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"testing/fstest"

	assets "github.com/Passion-Flow/ownsolid/services/control/migrations/mysql"
)

func TestMySQLNodeSchemaImmutablePrefixAndCurrentOwnership(t *testing.T) {
	c, e := DefaultCatalog()
	if e != nil || c.Steps() < 45 {
		t.Fatal("current node catalog", e)
	}
	raw, _ := assets.Files.ReadFile("evolution.json")
	cut := bytes.LastIndex(raw, []byte(",\n    {\n      \"step\": 42,"))
	if cut < 0 || hash(string(append(append([]byte{}, raw[:cut]...), []byte("\n  ]\n}\n")...))) != "442a24571cc04224b75d3c0c2f1a268706b2932deec7d0456470dd2b03f93df2" {
		t.Fatal("immutable1-41 serialized evolution prefix changed")
	}
	for index, object := range []string{"managed_nodes", "node_join_tokens", "node_tasks", "node_derived_inference_immutable"} {
		entry := c.steps[41+index]
		kind := createTable
		if index == 3 {
			kind = createTrigger
		}
		if entry.Object != object || entry.Kind != kind || entry.Step != 42+index {
			t.Fatal("node table/trigger dependency ownership changed")
		}
	}
}

func TestMySQLNodeSchemaGeneratedUniquenessAndCurrentBounds(t *testing.T) {
	c, e := DefaultCatalog()
	if e != nil {
		t.Fatal(e)
	}
	for index, tokens := range map[int][]string{
		41: {"`live_name` varchar(100)", "STORED", "UNIQUE KEY `nodes_live_name_idx`", "UNIQUE KEY `nodes_scope_identity`", "`credential_hash` varbinary(32)", "`inference_fenced_at` datetime(6)", "json_length(`capabilities`) <= 11", "docker_model_lifecycle"},
		42: {"`token_hash` varbinary(32) NOT NULL", "`expires_at` datetime(6) NOT NULL", "FOREIGN KEY (`tenant_id`, `workspace_id`, `node_id`) REFERENCES `managed_nodes`", "`execution_profile` varchar(32)"},
		43: {"`inflight_node_id` char(36)", "UNIQUE KEY `tasks_one_inflight_idx`", "UNIQUE KEY `tasks_idempotency_identity`", "UNIQUE KEY `tasks_scope_identity`", "`payload` json NOT NULL", "32768", "CONSTRAINT `node_derived_delegation_presence`", "`inference_requested_epoch` bigint", "between 0 and 3"},
	} {
		for _, token := range tokens {
			if !strings.Contains(c.steps[index].Definition, token) {
				t.Fatalf("measured node definition lost contract token %q", token)
			}
		}
	}
}

func TestMySQLNodeDerivedTriggerProtectsCurrentCommandIdentity(t *testing.T) {
	c, e := DefaultCatalog()
	if e != nil {
		t.Fatal(e)
	}
	entry := c.steps[44]
	d, _, e := canonicalTrigger(entry.Definition)
	if e != nil || d.Name != "node_derived_inference_immutable" || d.Table != "node_tasks" || d.Event != "UPDATE" || d.Timing != "BEFORE" || entry.sql != triggerSource(d) {
		t.Fatal("actual current node trigger context/source mismatch", e)
	}
	for _, field := range []string{"id", "tenant_id", "workspace_id", "node_id", "operation", "payload", "requested_by", "inference_requested_epoch", "idempotency_key", "request_fingerprint", "created_at"} {
		if !strings.Contains(d.Statement, "NOT (NEW."+field+" <=> OLD."+field+")") {
			t.Fatal("null-safe immutable command field missing", field)
		}
	}
	if strings.Contains(d.Statement, "NEW.inflight_node_id") || strings.Contains(d.Statement, "OLD.inflight_node_id") || !strings.Contains(d.Statement, "DERIVED_INFERENCE_COMMAND_IMMUTABLE") {
		t.Fatal("trigger references generated state or loses safe refusal")
	}
}

func TestMySQLNodeTriggerCatalogRefusesChangedContextAndSource(t *testing.T) {
	base, _ := assets.Files.ReadFile("reference.json")
	raw, _ := assets.Files.ReadFile("evolution.json")
	var original referenceDocument
	var evolution evolutionDocument
	json.Unmarshal(base, &original)
	json.Unmarshal(raw, &evolution)
	original.Steps = append(original.Steps, evolution.Steps...)
	for _, fault := range []string{"valid", "table", "event", "charset", "source"} {
		t.Run(fault, func(t *testing.T) {
			raw, _ := json.Marshal(original)
			var doc referenceDocument
			json.Unmarshal(raw, &doc)
			files := fstest.MapFS{}
			for _, item := range append([]reference{doc.Journal}, doc.Steps...) {
				body, e := assets.Files.ReadFile(item.File)
				if e != nil {
					t.Fatal(e)
				}
				files[item.File] = &fstest.MapFile{Data: body, Mode: 0444}
			}
			item := &doc.Steps[44]
			d, _, _ := canonicalTrigger(item.Definition)
			switch fault {
			case "table":
				d.Table = "users"
			case "event":
				d.Event = "INSERT"
			case "charset":
				d.CharacterSetClient = "latin1"
			case "source":
				files[item.File].Data = append(files[item.File].Data, []byte("DELETE FROM users;\n")...)
				item.SourceSHA256 = hash(string(files[item.File].Data))
			}
			if fault != "source" {
				encoded, _ := json.Marshal(d)
				item.Definition, item.DefinitionSHA256 = string(encoded), hash(string(encoded))
			}
			raw, _ = json.Marshal(doc)
			_, e := loadCatalog(raw, files)
			if fault == "valid" && e != nil || fault != "valid" && e == nil {
				t.Fatal("node trigger source/context refusal lost", e)
			}
		})
	}
}
