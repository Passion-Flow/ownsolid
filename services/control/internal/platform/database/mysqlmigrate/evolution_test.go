package mysqlmigrate

import (
	"encoding/json"
	"strings"
	"testing"
	"testing/fstest"
)

// These metadata/SQL fixtures are identified parser test doubles. Actual ALTER,
// journal reconciliation and business preservation require separate real SQL.
func evolutionFixture(t *testing.T) ([]byte, evolutionDocument, fstest.MapFS) {
	t.Helper()
	base, files := fixtureCatalog(t)
	raw, e := json.Marshal(base)
	if e != nil {
		t.Fatal(e)
	}
	previous := base.Steps[0]
	ddl := "ALTER TABLE tenants MODIFY name VARCHAR(101) NOT NULL;\n"
	definition := strings.Replace(previous.Definition, "`name` varchar(100)", "`name` varchar(101)", 1)
	if definition == previous.Definition {
		t.Fatal("parser fixture definition did not change")
	}
	item := reference{Step: 16, File: "000016_tenants_name.sql", SourceSHA256: hash(ddl), Object: "tenants", Kind: alterTable, PreviousDefinitionSHA256: previous.DefinitionSHA256, Definition: definition, DefinitionSHA256: hash(definition)}
	files[item.File] = &fstest.MapFile{Data: []byte(ddl), Mode: 0444}
	return raw, evolutionDocument{Format: evolutionFormat, BaseReferenceSHA256: hash(string(raw)), Steps: []reference{item}}, files
}

func TestMySQLEvolutionPredecessorChainAndAppliedPrefix(t *testing.T) {
	raw, extension, files := evolutionFixture(t)
	encoded, e := json.Marshal(extension)
	if e != nil {
		t.Fatal(e)
	}
	c, e := loadEvolution(raw, encoded, files)
	if e != nil || c.Steps() != 16 || c.steps[15].PreviousDefinitionSHA256 != c.steps[0].DefinitionSHA256 {
		t.Fatal("explicit evolution chain", e)
	}
	for _, prefix := range []int{0, 1, 14, 15, 16} {
		latest := latestTableSteps(c, prefix)
		if prefix == 0 {
			if len(latest) != 0 {
				t.Fatal("empty prefix used future metadata")
			}
			continue
		}
		wanted := 0
		if prefix == 16 {
			wanted = 15
		}
		if latest["tenants"] != wanted {
			t.Fatal("wrong applied-prefix definition", prefix, latest["tenants"])
		}
		if _, present := latest["installation_seed"]; present {
			t.Fatal("seed treated as a real table")
		}
		if prefix >= 14 && len(latest) != 14 {
			t.Fatal("evolution changed table ownership inventory")
		}
	}
	// A second legitimate change must reference the first change,not the base.
	second := extension.Steps[0]
	second.Step = 17
	second.File = "000017_tenants_name.sql"
	second.PreviousDefinitionSHA256 = second.DefinitionSHA256
	second.Definition = strings.Replace(second.Definition, "varchar(101)", "varchar(102)", 1)
	second.DefinitionSHA256 = hash(second.Definition)
	ddl := "ALTER TABLE tenants MODIFY name VARCHAR(102) NOT NULL;"
	second.SourceSHA256 = hash(ddl)
	files[second.File] = &fstest.MapFile{Data: []byte(ddl)}
	extension.Steps = append(extension.Steps, second)
	encoded, _ = json.Marshal(extension)
	if c, e := loadEvolution(raw, encoded, files); e != nil || latestTableSteps(c, 17)["tenants"] != 16 {
		t.Fatal("second chained change", e)
	}
}

func TestMySQLEvolutionRejectsUnboundMissingOrUnsafeTransitions(t *testing.T) {
	for name, change := range map[string]func(*evolutionDocument, fstest.MapFS){
		"base-binding":        func(d *evolutionDocument, _ fstest.MapFS) { d.BaseReferenceSHA256 = strings.Repeat("0", 64) },
		"empty":               func(d *evolutionDocument, _ fstest.MapFS) { d.Steps = nil },
		"format":              func(d *evolutionDocument, _ fstest.MapFS) { d.Format = "unowned" },
		"sequence":            func(d *evolutionDocument, _ fstest.MapFS) { d.Steps[0].Step = 17 },
		"missing-predecessor": func(d *evolutionDocument, _ fstest.MapFS) { d.Steps[0].PreviousDefinitionSHA256 = "" },
		"wrong-predecessor": func(d *evolutionDocument, _ fstest.MapFS) {
			d.Steps[0].PreviousDefinitionSHA256 = strings.Repeat("1", 64)
		},
		"no-op": func(d *evolutionDocument, _ fstest.MapFS) {
			d.Steps[0].PreviousDefinitionSHA256 = d.Steps[0].DefinitionSHA256
		},
		"unknown-kind":     func(d *evolutionDocument, _ fstest.MapFS) { d.Steps[0].Kind = "run_sql" },
		"missing-source":   func(d *evolutionDocument, f fstest.MapFS) { delete(f, d.Steps[0].File) },
		"source-drift":     func(d *evolutionDocument, f fstest.MapFS) { f[d.Steps[0].File].Data = []byte("DROP TABLE tenants;") },
		"definition-drift": func(d *evolutionDocument, _ fstest.MapFS) { d.Steps[0].Definition += " changed" },
		"alter-before-create": func(d *evolutionDocument, f fstest.MapFS) {
			item := &d.Steps[0]
			item.Object = "unowned"
			item.Definition = strings.Replace(item.Definition, "`tenants`", "`unowned`", 1)
			item.DefinitionSHA256 = hash(item.Definition)
			ddl := "ALTER TABLE unowned MODIFY name VARCHAR(101) NOT NULL;"
			item.SourceSHA256 = hash(ddl)
			f[item.File].Data = []byte(ddl)
		},
		"duplicate-create": func(d *evolutionDocument, f fstest.MapFS) {
			item := &d.Steps[0]
			item.Kind = createTable
			item.PreviousDefinitionSHA256 = ""
			ddl := "CREATE TABLE tenants (id INT);"
			item.SourceSHA256 = hash(ddl)
			f[item.File].Data = []byte(ddl)
		},
		"source-object": func(d *evolutionDocument, f fstest.MapFS) {
			item := &d.Steps[0]
			ddl := "ALTER TABLE users MODIFY name VARCHAR(101) NOT NULL;"
			item.SourceSHA256 = hash(ddl)
			f[item.File].Data = []byte(ddl)
		},
	} {
		t.Run(name, func(t *testing.T) {
			raw, extension, files := evolutionFixture(t)
			change(&extension, files)
			encoded, _ := json.Marshal(extension)
			if _, e := loadEvolution(raw, encoded, files); e == nil || e.Error() != CodeCatalogInvalid {
				t.Fatal("unsafe catalog admitted", e)
			}
		})
	}
}

func TestMySQLEvolutionRejectsAmbiguousMetadata(t *testing.T) {
	raw, extension, files := evolutionFixture(t)
	encoded, _ := json.Marshal(extension)
	for _, invalid := range [][]byte{nil, []byte("null"), append([]byte(`{"format":"duplicate",`), encoded[1:]...), append(encoded[:len(encoded)-1:len(encoded)-1], []byte(`,"unknown":true}`)...), []byte(strings.Repeat(" ", maximumCatalogBytes+1))} {
		if _, e := loadEvolution(raw, invalid, files); e == nil {
			t.Fatal("ambiguous evolution metadata admitted")
		}
	}
}
