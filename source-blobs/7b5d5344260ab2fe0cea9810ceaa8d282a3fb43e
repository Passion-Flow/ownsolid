package mysqlmigrate

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"testing/fstest"

	assets "github.com/Passion-Flow/ownsolid/services/control/migrations/mysql"
)

func TestMySQLReceiptIndexImmutableCatalogAppend(t *testing.T) {
	c, e := DefaultCatalog()
	if e != nil || c.Steps() < 36 {
		t.Fatal("actual index catalog", e)
	}
	last := c.steps[35]
	if last.Step != 36 || last.Object != "audit_events" || last.Kind != alterTable || last.File != "000036_audit_operation_lookup.sql" || last.sql != "ALTER TABLE audit_events ADD INDEX audit_operation_lookup_idx(request_id,actor_user_id,audience,tenant_id), ALGORITHM=INPLACE, LOCK=NONE;" {
		t.Fatal("unowned index migration")
	}
	if !strings.Contains(last.Definition, "KEY `audit_operation_lookup_idx` (`request_id`,`actor_user_id`,`audience`,`tenant_id`)") || last.PreviousDefinitionSHA256 != c.steps[10].DefinitionSHA256 {
		t.Fatal("ordered nonunique index definition or predecessor changed")
	}
	raw, e := assets.Files.ReadFile("evolution.json")
	if e != nil {
		t.Fatal(e)
	}
	cut := bytes.LastIndex(raw, []byte(",\n    {\n      \"step\": 36,"))
	if cut < 0 {
		t.Fatal("canonical append boundary missing")
	}
	previous := append(append([]byte{}, raw[:cut]...), []byte("\n  ]\n}\n")...)
	if hash(string(previous)) != "d4be641c33ccd10879ef960cd3204b9ebc1f77856b7fefbe0099b36cd786e33e" {
		t.Fatal("immutable steps1–35 metadata bytes changed")
	}
}

func TestMySQLReceiptIndexUnsafeCatalogInputsRefuse(t *testing.T) {
	base, _ := assets.Files.ReadFile("reference.json")
	evolved, _ := assets.Files.ReadFile("evolution.json")
	var original referenceDocument
	var extension evolutionDocument
	if json.Unmarshal(base, &original) != nil || json.Unmarshal(evolved, &extension) != nil {
		t.Fatal("reference decode")
	}
	original.Steps = append(original.Steps, extension.Steps...)
	// This suite owns the frozen receipt-index prefix,not future migrations.
	original.Steps = original.Steps[:36]
	for _, name := range []string{"source_hash", "wrong_predecessor", "unowned_table_token", "unchanged_definition", "definition_hash", "extra_statement"} {
		t.Run(name, func(t *testing.T) {
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
			last := &doc.Steps[len(doc.Steps)-1]
			switch name {
			case "source_hash":
				last.SourceSHA256 = hash("unrelated source")
			case "wrong_predecessor":
				last.PreviousDefinitionSHA256 = hash("unrelated table")
			case "unowned_table_token":
				files[last.File].Data = []byte("ALTER TABLE audit_events_other ADD INDEX test_index(request_id);\n")
				last.SourceSHA256 = hash(string(files[last.File].Data))
			case "unchanged_definition":
				last.Definition = doc.Steps[10].Definition
				last.DefinitionSHA256 = last.PreviousDefinitionSHA256
			case "definition_hash":
				last.DefinitionSHA256 = hash("unrelated definition")
			case "extra_statement":
				files[last.File].Data = append(files[last.File].Data, []byte("DROP TABLE audit_events;\n")...)
				last.SourceSHA256 = hash(string(files[last.File].Data))
			}
			raw, _ = json.Marshal(doc)
			if _, e := loadCatalog(raw, files); e == nil {
				t.Fatal("unsafe index catalog admitted")
			}
		})
	}
}
