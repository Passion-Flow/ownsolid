package mysqlmigrate

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"testing/fstest"

	assets "github.com/Passion-Flow/ownsolid/services/control/migrations/mysql"
)

func TestMySQLLicenseSchemaImmutablePrefixAndCurrentOwnedContract(t *testing.T) {
	c, e := DefaultCatalog()
	if e != nil || c.Steps() < 41 {
		t.Fatal("current License catalog", e)
	}
	raw, _ := assets.Files.ReadFile("evolution.json")
	cut := bytes.LastIndex(raw, []byte(",\n    {\n      \"step\": 39,"))
	if cut < 0 || hash(string(append(append([]byte{}, raw[:cut]...), []byte("\n  ]\n}\n")...))) != "bcd04b932a64b07f8d848960f699d0431c450a818035212f65fe15cf0afc74ea" {
		t.Fatal("immutable1–38 serialized evolution prefix changed")
	}
	state, seed, artifacts := c.steps[38], c.steps[39], c.steps[40]
	if state.Object != "platform_license_state" || state.Kind != createTable || seed.Object != licenseStateSeedObject || seed.Kind != installationSeed || seed.sql != "INSERT INTO platform_license_state(id) VALUES(1);" || seed.Definition != "EXISTS platform_license_state id=1" || artifacts.Object != "platform_license_artifacts" || artifacts.Kind != createTable {
		t.Fatal("current exact state/seed/history ownership or order changed")
	}
	for _, token := range []string{"`deployment_id` char(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT (uuid())", "`observed_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)", "CONSTRAINT `license_deployment_canonical`", "CONSTRAINT `license_observed_range`"} {
		if !strings.Contains(state.Definition, token) {
			t.Fatal("actual measured deployment identity contract lost")
		}
	}
	for _, token := range []string{"`artifact` mediumblob NOT NULL", "`reason` varchar(500)", "UNIQUE KEY `sequence`", "UNIQUE KEY `artifact_sha256`", "FOREIGN KEY (`deployment_id`) REFERENCES `platform_license_state` (`deployment_id`)", "FOREIGN KEY (`predecessor_id`) REFERENCES `platform_license_artifacts` (`license_id`)", "FOREIGN KEY (`imported_by`) REFERENCES `users` (`id`)", "CONSTRAINT `license_predecessor_distinct`", "between 1 and 65536", "between 1 and 1024"} {
		if !strings.Contains(artifacts.Definition, token) {
			t.Fatal("actual measured artifact history bounds/foreign keys lost")
		}
	}
}

func TestMySQLLicenseSeedCatalogRefusesArbitrarySourceOrMissingTable(t *testing.T) {
	base, _ := assets.Files.ReadFile("reference.json")
	raw, _ := assets.Files.ReadFile("evolution.json")
	var original referenceDocument
	var extension evolutionDocument
	json.Unmarshal(base, &original)
	json.Unmarshal(raw, &extension)
	original.Steps = append(original.Steps, extension.Steps...)
	for _, fault := range []string{"valid", "arbitrary_sql", "wrong_object", "wrong_condition", "unexpected_predecessor", "missing_state_table", "extra_statement", "wrong_source_hash"} {
		t.Run(fault, func(t *testing.T) {
			raw, _ := json.Marshal(original)
			var d referenceDocument
			json.Unmarshal(raw, &d)
			files := fstest.MapFS{}
			for _, entry := range append([]reference{d.Journal}, d.Steps...) {
				body, e := assets.Files.ReadFile(entry.File)
				if e != nil {
					t.Fatal(e)
				}
				files[entry.File] = &fstest.MapFile{Data: body, Mode: 0444}
			}
			seed := &d.Steps[39]
			switch fault {
			case "arbitrary_sql":
				files[seed.File].Data = []byte("UPDATE users SET is_platform_admin=true;\n")
				seed.SourceSHA256 = hash(string(files[seed.File].Data))
			case "wrong_object":
				seed.Object = "unowned_seed"
			case "wrong_condition":
				seed.Definition = "EXISTS installation_state id=1"
				seed.DefinitionSHA256 = hash(seed.Definition)
			case "unexpected_predecessor":
				seed.PreviousDefinitionSHA256 = d.Steps[38].DefinitionSHA256
			case "missing_state_table":
				state := &d.Steps[38]
				state.Object = "license_state_other"
				files[state.File].Data = bytes.ReplaceAll(files[state.File].Data, []byte("platform_license_state"), []byte(state.Object))
				state.SourceSHA256 = hash(string(files[state.File].Data))
				state.Definition = strings.ReplaceAll(state.Definition, "platform_license_state", state.Object)
				state.DefinitionSHA256 = hash(state.Definition)
			case "extra_statement":
				files[seed.File].Data = append(files[seed.File].Data, []byte("DELETE FROM users;\n")...)
				seed.SourceSHA256 = hash(string(files[seed.File].Data))
			case "wrong_source_hash":
				seed.SourceSHA256 = hash("unowned source")
			}
			raw, _ = json.Marshal(d)
			_, e := loadCatalog(raw, files)
			if fault == "valid" && e != nil || fault != "valid" && e == nil {
				t.Fatal("License seed catalog boundary changed", e)
			}
		})
	}
}

func TestMySQLLicenseSeedCanonicalizationRemainsNarrow(t *testing.T) {
	d := evolutionDocument{Format: evolutionFormat, BaseReferenceSHA256: hash("synthetic base"), Steps: []reference{{Step: 40, File: "000040_platform_license_seed.sql", SourceSHA256: hash(licenseStateSeedSQL + "\n"), Object: licenseStateSeedObject, Kind: installationSeed, Definition: licenseStateSeedDefinition, DefinitionSHA256: hash(licenseStateSeedDefinition)}}}
	for _, fault := range []string{"valid", "arbitrary_object", "arbitrary_condition", "predecessor"} {
		t.Run(fault, func(t *testing.T) {
			changed := d
			changed.Steps = append([]reference{}, d.Steps...)
			switch fault {
			case "arbitrary_object":
				changed.Steps[0].Object = "installation_seed"
			case "arbitrary_condition":
				changed.Steps[0].Definition = "EXISTS users id=1"
				changed.Steps[0].DefinitionSHA256 = hash(changed.Steps[0].Definition)
			case "predecessor":
				changed.Steps[0].PreviousDefinitionSHA256 = hash("unowned predecessor")
			}
			raw, _ := json.Marshal(changed)
			_, e := CanonicalizeMeasuredEvolution(raw)
			if fault == "valid" && e != nil || fault != "valid" && e == nil {
				t.Fatal("License seed measurement boundary changed", e)
			}
		})
	}
}
