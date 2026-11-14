package mysqlmigrate

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
)

func triggerUnitDefinition() triggerDefinition {
	return triggerDefinition{Format: triggerDefinitionFormat, Name: "synthetic_tenant_guard", Table: "tenants", Timing: "BEFORE", Event: "DELETE", Orientation: "ROW", Order: 1, Statement: "BEGIN\n    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='Synthetic \u4e16\u754c';\nEND", SQLModes: strings.Split(mysqlstore.SessionSQLModes, ","), CharacterSetClient: mysqlstore.ConnectionCharset, CollationConnection: mysqlstore.ConnectionCollation, DatabaseCollation: mysqlstore.ConnectionCollation}
}

func triggerUnitReference(t *testing.T) ([]byte, evolutionDocument, fstest.MapFS) {
	t.Helper()
	base, files := fixtureCatalog(t)
	raw, e := json.Marshal(base)
	if e != nil {
		t.Fatal(e)
	}
	d := triggerUnitDefinition()
	measured, _ := json.Marshal(d)
	_, canonical, e := canonicalTrigger(string(measured))
	if e != nil {
		t.Fatal(e)
	}
	source := triggerSource(d) + "\n"
	item := reference{Step: 16, File: "000016_synthetic_tenant_guard.sql", Object: d.Name, Kind: createTrigger, SourceSHA256: hash(source), Definition: canonical, DefinitionSHA256: hash(canonical)}
	files[item.File] = &fstest.MapFile{Data: []byte(source)}
	return raw, evolutionDocument{Format: evolutionFormat, BaseReferenceSHA256: hash(string(raw)), Steps: []reference{item}}, files
}

func TestMySQLTriggerCanonicalMetadata(t *testing.T) {
	d := triggerUnitDefinition()
	raw, _ := json.Marshal(d)
	got, canonical, e := canonicalTrigger(string(raw))
	if e != nil || got.Statement != d.Statement || !slices.IsSorted(got.SQLModes) || !strings.Contains(canonical, "\u4e16\u754c") {
		t.Fatal("trigger metadata altered source/context", e)
	}
	if _, repeated, e := canonicalTrigger(canonical); e != nil || canonical != repeated {
		t.Fatal("trigger metadata canonicalization not stable", e)
	}
	for _, principal := range []string{"operator@%", "schema_user@localhost", "migrator@10.0.%", "migrator@::1"} {
		if !authenticatedDefiner.MatchString(principal) {
			t.Fatal("supported authenticated principal refused")
		}
	}
	for _, principal := range []string{"", "@localhost", "operator", "operator@", "operator@localhost\n", "operator@bad host", "operator@host@other"} {
		if authenticatedDefiner.MatchString(principal) {
			t.Fatal("ambiguous principal accepted")
		}
	}
}

func TestMySQLTriggerRejectsMetadataDrift(t *testing.T) {
	changes := map[string]func(*triggerDefinition){
		"format":         func(d *triggerDefinition) { d.Format = "unknown" },
		"name":           func(d *triggerDefinition) { d.Name = "bad`name" },
		"namespace":      func(d *triggerDefinition) { d.Name = d.Table },
		"table":          func(d *triggerDefinition) { d.Table = "other.table" },
		"timing":         func(d *triggerDefinition) { d.Timing = "AFTER" },
		"event":          func(d *triggerDefinition) { d.Event = "UPSERT" },
		"orientation":    func(d *triggerDefinition) { d.Orientation = "STATEMENT" },
		"order":          func(d *triggerDefinition) { d.Order = 2 },
		"mode_missing":   func(d *triggerDefinition) { d.SQLModes = d.SQLModes[1:] },
		"mode_duplicate": func(d *triggerDefinition) { d.SQLModes = append(d.SQLModes, d.SQLModes[0]) },
		"charset":        func(d *triggerDefinition) { d.CharacterSetClient = "latin1" },
		"connection":     func(d *triggerDefinition) { d.CollationConnection = "utf8mb4_0900_ai_ci" },
		"database":       func(d *triggerDefinition) { d.DatabaseCollation = "utf8mb4_0900_ai_ci" },
		"body_empty":     func(d *triggerDefinition) { d.Statement = "" },
		"body_wrapper":   func(d *triggerDefinition) { d.Statement = "BEGIN\nEND;\nDROP TABLE tenants;\nBEGIN\nEND" },
		"body_CR":        func(d *triggerDefinition) { d.Statement = strings.ReplaceAll(d.Statement, "\n", "\r\n") },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			d := triggerUnitDefinition()
			change(&d)
			raw, _ := json.Marshal(d)
			if _, _, e := canonicalTrigger(string(raw)); e == nil {
				t.Fatal("invalid trigger metadata accepted")
			}
		})
	}
	d := triggerUnitDefinition()
	raw, _ := json.Marshal(d)
	for _, invalid := range []string{"null", strings.TrimSuffix(string(raw), "}") + `,"definer":"root@localhost"}`, strings.TrimSuffix(string(raw), "}") + `,"order":1}`} {
		if _, _, e := canonicalTrigger(invalid); e == nil {
			t.Fatal("unknown/duplicate/null metadata accepted")
		}
	}
}

func TestMySQLTriggerCatalogOwnership(t *testing.T) {
	raw, document, files := triggerUnitReference(t)
	encoded, _ := json.Marshal(document)
	c, e := loadEvolution(raw, encoded, files)
	if e != nil || c.Steps() != 16 || c.steps[15].Kind != createTrigger || latestTableSteps(c, 16)[document.Steps[0].Object] != 15 {
		t.Fatal("owned independent trigger was skipped", e)
	}
	if _, exists := latestTableSteps(c, 15)[document.Steps[0].Object]; exists {
		t.Fatal("future trigger leaked into an old applied prefix")
	}
	canonical, e := CanonicalizeMeasuredEvolution(encoded)
	if e != nil {
		t.Fatal(e)
	}
	var normalized evolutionDocument
	if json.Unmarshal(canonical, &normalized) != nil || normalized.Steps[0].Definition != document.Steps[0].Definition {
		t.Fatal("trigger reference changed measured statement")
	}
}

func TestMySQLTriggerRejectsSourceOwnership(t *testing.T) {
	for _, name := range []string{"owner_override", "extra_statement", "delimiter", "event", "unknown_table", "previous_hash", "table_kind"} {
		t.Run(name, func(t *testing.T) {
			raw, document, files := triggerUnitReference(t)
			item := &document.Steps[0]
			source := string(files[item.File].Data)
			switch name {
			case "owner_override":
				source = strings.Replace(source, "CREATE TRIGGER", "CREATE DEFINER=CURRENT_USER TRIGGER", 1)
			case "extra_statement":
				source += "DROP TABLE tenants;"
			case "delimiter":
				source = "DELIMITER $$\n" + source
			case "event":
				source = strings.Replace(source, "BEFORE DELETE", "BEFORE UPDATE", 1)
			case "unknown_table":
				var d triggerDefinition
				json.Unmarshal([]byte(item.Definition), &d)
				d.Table = "not_owned"
				metadata, _ := json.Marshal(d)
				_, item.Definition, _ = canonicalTrigger(string(metadata))
				item.DefinitionSHA256 = hash(item.Definition)
				source = triggerSource(d) + "\n"
			case "previous_hash":
				item.PreviousDefinitionSHA256 = strings.Repeat("0", 64)
			case "table_kind":
				item.Kind = createTable
			}
			files[item.File].Data = []byte(source)
			item.SourceSHA256 = hash(source)
			encoded, _ := json.Marshal(document)
			if _, e := loadEvolution(raw, encoded, files); e == nil {
				t.Fatal("wrong trigger source/ownership accepted")
			}
		})
	}
}
