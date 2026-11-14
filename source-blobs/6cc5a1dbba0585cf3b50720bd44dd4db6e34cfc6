package mysqlmigrate

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	assets "github.com/Passion-Flow/ownsolid/services/control/migrations/mysql"
)

func activeVariantFixture(t *testing.T) ([]byte, evolutionDocument, fstest.MapFS) {
	t.Helper()
	base, e := assets.Files.ReadFile("reference.json")
	if e != nil {
		t.Fatal(e)
	}
	raw, e := assets.Files.ReadFile("evolution.json")
	var document evolutionDocument
	if e != nil || json.Unmarshal(raw, &document) != nil {
		t.Fatal("active variant metadata", e)
	}
	files := fstest.MapFS{}
	names, e := fs.Glob(assets.Files, "*.sql")
	if e != nil {
		t.Fatal(e)
	}
	for _, name := range names {
		raw, e := assets.Files.ReadFile(name)
		if e != nil {
			t.Fatal(e)
		}
		files[name] = &fstest.MapFile{Data: raw, Mode: 0444}
	}
	return base, document, files
}

func TestMySQLVariantCatalogPreserves203AndAdmitsMeasured265(t *testing.T) {
	raw, e := assets.Files.ReadFile("evolution.json")
	if e != nil {
		t.Fatal(e)
	}
	cut := bytes.LastIndex(raw, []byte(",\n    {\n      \"step\": 204,"))
	if cut < 0 || hash(string(append(append([]byte{}, raw[:cut]...), []byte("\n  ]\n}\n")...))) != "a29521fd9af2dca5ec25fa99ec6dd06e1d23fe6986e9a27933a5d3880cce59c6" {
		t.Fatal("immutable203 metadata prefix changed")
	}
	base, document, files := activeVariantFixture(t)
	if hash(string(base)) != "e7f70ebfe7f72bff01d234203d6276764101f8fa8f6b5e34311e1a364bed1392" {
		t.Fatal("immutable15 reference changed")
	}
	encoded, e := json.Marshal(document)
	if e != nil {
		t.Fatal(e)
	}
	catalog, e := loadEvolution(base, encoded, files)
	if e != nil || catalog.Steps() != 265 {
		t.Fatal("measured265 active catalog refused", e)
	}
	counts := map[string]int{}
	for _, item := range catalog.steps[203:] {
		counts[item.Kind]++
	}
	if counts[createTable] != 22 || counts[alterTable] != 9 || counts[createTrigger] != 31 || len(counts) != 3 {
		t.Fatal("measured variant statement boundaries changed", counts)
	}
	if migrationFixtureMaximumSteps < catalog.Steps() || testOptions().Validate() != nil {
		t.Fatal("declared fixture cannot admit current catalog")
	}
}

func TestMySQLVariantCatalogRejectsSourcePredecessorAndTriggerContextDrift(t *testing.T) {
	for _, name := range []string{"source", "definition", "predecessor", "trigger_context", "trigger_source", "missing", "extra"} {
		t.Run(name, func(t *testing.T) {
			base, document, files := activeVariantFixture(t)
			var table, alter, trigger int
			for n := 188; n < len(document.Steps); n++ {
				switch document.Steps[n].Kind {
				case createTable:
					table = n
				case alterTable:
					alter = n
				case createTrigger:
					trigger = n
				}
			}
			switch name {
			case "source":
				files[document.Steps[table].File].Data = []byte("SELECT 1;")
			case "definition":
				document.Steps[table].Definition += " changed"
			case "predecessor":
				document.Steps[alter].PreviousDefinitionSHA256 = strings.Repeat("a", 64)
			case "trigger_context":
				item := &document.Steps[trigger]
				var definition triggerDefinition
				if json.Unmarshal([]byte(item.Definition), &definition) != nil {
					t.Fatal("frozen trigger metadata")
				}
				definition.DatabaseCollation = "utf8mb4_general_ci"
				raw, e := json.Marshal(definition)
				if e != nil {
					t.Fatal(e)
				}
				item.Definition = string(raw)
				item.DefinitionSHA256 = hash(item.Definition)
			case "trigger_source":
				item := &document.Steps[trigger]
				raw := string(files[item.File].Data)
				raw = strings.Replace(raw, "PRIVATE_CLUSTER_IMMUTABLE_HISTORY", "UNOWNED_MUTATION", 1)
				files[item.File].Data = []byte(raw)
				item.SourceSHA256 = hash(raw)
			case "missing":
				delete(files, document.Steps[table].File)
			case "extra":
				files["000266_unregistered.sql"] = &fstest.MapFile{Data: []byte("SELECT 1;"), Mode: 0444}
			}
			raw, e := json.Marshal(document)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = loadEvolution(base, raw, files); e == nil || e.Error() != CodeCatalogInvalid {
				t.Fatal("variant catalog drift admitted", e)
			}
		})
	}
}
