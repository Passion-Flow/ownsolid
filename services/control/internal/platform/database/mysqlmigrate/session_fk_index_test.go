package mysqlmigrate

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"testing/fstest"

	assets "github.com/Passion-Flow/ownsolid/services/control/migrations/mysql"
)

func TestMySQLSessionForeignKeyIndexImmutableAppendAndPredecessors(t *testing.T) {
	c, e := DefaultCatalog()
	if e != nil || c.Steps() < 38 {
		t.Fatal("current session index catalog", e)
	}
	raw, _ := assets.Files.ReadFile("evolution.json")
	cut := bytes.LastIndex(raw, []byte(",\n    {\n      \"step\": 37,"))
	if cut < 0 || hash(string(append(append([]byte{}, raw[:cut]...), []byte("\n  ]\n}\n")...))) != "b933c1ecff6cc02d12f896155a8f4a2be4498bd9c4d4a9c0fd86007ef6488fde" {
		t.Fatal("immutable1–36 serialized evolution prefix changed")
	}
	first, final := c.steps[36], c.steps[37]
	if first.Object != "sessions" || final.Object != first.Object || first.Kind != alterTable || final.Kind != alterTable || first.PreviousDefinitionSHA256 != c.steps[21].DefinitionSHA256 || final.PreviousDefinitionSHA256 != first.DefinitionSHA256 {
		t.Fatal("session-only measured predecessor chain changed")
	}
	if first.sql != "ALTER TABLE sessions ADD INDEX sessions_user_fk_idx(user_id), DROP INDEX sessions_user_live_idx, ALGORITHM=INPLACE, LOCK=NONE;" || final.sql != "ALTER TABLE sessions ADD INDEX sessions_user_live_idx(user_id,revoked_at), ALGORITHM=INPLACE, LOCK=NONE;" {
		t.Fatal("distinct engine DDL boundaries changed")
	}
	stable := "KEY `sessions_user_fk_idx` (`user_id`)"
	live := "KEY `sessions_user_live_idx` (`user_id`,`revoked_at`)"
	if !strings.Contains(first.Definition, stable) || strings.Contains(first.Definition, live) || !strings.Contains(final.Definition, stable) || !strings.Contains(final.Definition, live) || strings.Index(final.Definition, stable) >= strings.Index(final.Definition, live) {
		t.Fatal("stable FK index must precede retained live-query index")
	}
	for _, token := range []string{"CONSTRAINT `sessions_ibfk_1` FOREIGN KEY (`user_id`)", "CONSTRAINT `sessions_sso_binding_fk`", "CONSTRAINT `sessions_sso_flow_fk`", "CONSTRAINT `sessions_sso_source_complete`", "UNIQUE KEY `sessions_sso_flow_unique`"} {
		if !strings.Contains(first.Definition, token) || !strings.Contains(final.Definition, token) {
			t.Fatal("session source/foreign-key/single-issuance constraint lost")
		}
	}
}

func TestMySQLSessionForeignKeyIndexUnsafeAppendRefuses(t *testing.T) {
	base, _ := assets.Files.ReadFile("reference.json")
	evolution, _ := assets.Files.ReadFile("evolution.json")
	var document referenceDocument
	var extension evolutionDocument
	json.Unmarshal(base, &document)
	json.Unmarshal(evolution, &extension)
	document.Steps = append(document.Steps, extension.Steps...)
	for _, fault := range []string{"wrong_predecessor37", "wrong_predecessor38", "unowned_table", "extra_statement", "source_hash", "unchanged_definition"} {
		t.Run(fault, func(t *testing.T) {
			raw, _ := json.Marshal(document)
			var changed referenceDocument
			json.Unmarshal(raw, &changed)
			files := fstest.MapFS{}
			for _, item := range append([]reference{changed.Journal}, changed.Steps...) {
				body, e := assets.Files.ReadFile(item.File)
				if e != nil {
					t.Fatal(e)
				}
				files[item.File] = &fstest.MapFile{Data: body, Mode: 0444}
			}
			first, final := &changed.Steps[36], &changed.Steps[37]
			switch fault {
			case "wrong_predecessor37":
				first.PreviousDefinitionSHA256 = changed.Steps[6].DefinitionSHA256
			case "wrong_predecessor38":
				final.PreviousDefinitionSHA256 = changed.Steps[21].DefinitionSHA256
			case "unowned_table":
				files[final.File].Data = []byte("ALTER TABLE sessions_other ADD INDEX sessions_user_live_idx(user_id,revoked_at);\n")
				final.SourceSHA256 = hash(string(files[final.File].Data))
			case "extra_statement":
				files[final.File].Data = append(files[final.File].Data, []byte("DROP TABLE sessions;\n")...)
				final.SourceSHA256 = hash(string(files[final.File].Data))
			case "source_hash":
				final.SourceSHA256 = hash("unowned SQL")
			case "unchanged_definition":
				final.Definition, final.DefinitionSHA256 = first.Definition, first.DefinitionSHA256
			}
			raw, _ = json.Marshal(changed)
			if _, e := loadCatalog(raw, files); e == nil {
				t.Fatal("unsafe session index append accepted")
			}
		})
	}
}
