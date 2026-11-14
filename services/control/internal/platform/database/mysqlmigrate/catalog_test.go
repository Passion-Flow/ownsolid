package mysqlmigrate

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"testing/fstest"

	assets "github.com/Passion-Flow/ownsolid/services/control/migrations/mysql"
)

func fixtureCatalog(t *testing.T) (referenceDocument, fstest.MapFS) {
	t.Helper()
	raw, e := assets.Files.ReadFile("reference.json")
	if e != nil {
		t.Fatal(e)
	}
	var document referenceDocument
	if json.Unmarshal(raw, &document) != nil {
		t.Fatal("reference decode")
	}
	files := fstest.MapFS{}
	list := []reference{document.Journal}
	list = append(list, document.Steps...)
	for _, item := range list {
		name := item.File
		bytes, e := assets.Files.ReadFile(name)
		if e != nil {
			t.Fatal(e)
		}
		files[name] = &fstest.MapFile{Data: bytes, Mode: 0444}
	}
	return document, files
}

func TestMySQLMigrationCatalogActualEmbeddedAssets(t *testing.T) {
	catalog, e := DefaultCatalog()
	if e != nil || catalog.Steps() != 265 || catalog.Version() != "8.4.12" || catalog.Scope() != "account_workspace_schema_only_product_pending" {
		t.Fatal("current complete shared business schema and immutable reference identity", e)
	}
	if catalog.steps[14].Kind != installationSeed || catalog.steps[7].Object != "invitations" || !strings.Contains(catalog.steps[7].Definition, "invitations_pending_email_idx") {
		t.Fatal("owned schema/seed/partial uniqueness contract")
	}
	if catalog.steps[15].Kind != alterTable || catalog.steps[15].PreviousDefinitionSHA256 != catalog.steps[2].DefinitionSHA256 || !strings.Contains(catalog.steps[17].Definition, "mail_deliveries_locale_check") {
		t.Fatal("current appended locale ownership contract")
	}
	if catalog.steps[18].Object != "sso_connectors" || catalog.steps[20].Object != "sso_flows" || catalog.steps[21].Kind != alterTable || catalog.steps[21].PreviousDefinitionSHA256 != catalog.steps[6].DefinitionSHA256 || !strings.Contains(catalog.steps[21].Definition, "sessions_sso_flow_fk") || catalog.steps[22].Object != "sso_mutation_intents" {
		t.Fatal("current SSO/session append ownership contract")
	}
	if catalog.steps[32].Object != "workspace_memberships" || catalog.steps[32].Kind != alterTable || catalog.steps[33].Object != membershipAuthoritySeedObject || catalog.steps[33].Kind != installationSeed || catalog.steps[34].Kind != alterTable || catalog.steps[34].PreviousDefinitionSHA256 != catalog.steps[32].DefinitionSHA256 {
		t.Fatal("current membership lifetime sequence missing")
	}
	for _, item := range catalog.steps[23:32] {
		if item.Kind != createTrigger {
			t.Fatal("current independent trigger step missing")
		}
	}
}

func TestMySQLMigrationCatalogRejectsSourceDefinitionAndSequenceDrift(t *testing.T) {
	for name, change := range map[string]func(*referenceDocument, fstest.MapFS){
		"source":  func(d *referenceDocument, f fstest.MapFS) { f[d.Steps[0].File].Data = []byte("SELECT 1;") },
		"missing": func(d *referenceDocument, f fstest.MapFS) { delete(f, d.Steps[0].File) },
		"extra": func(d *referenceDocument, f fstest.MapFS) {
			f["999999_unowned.sql"] = &fstest.MapFile{Data: []byte("SELECT 1;")}
		},
		"definition": func(d *referenceDocument, _ fstest.MapFS) { d.Steps[0].Definition += " changed" },
		"sequence":   func(d *referenceDocument, _ fstest.MapFS) { d.Steps[0].Step = 2 },
		"object":     func(d *referenceDocument, _ fstest.MapFS) { d.Steps[0].Object = "escape` injection" },
		"filename":   func(d *referenceDocument, _ fstest.MapFS) { d.Steps[0].File = "../journal.sql" },
		"duplicate":  func(d *referenceDocument, _ fstest.MapFS) { d.Steps[1].File = d.Steps[0].File },
		"journal":    func(d *referenceDocument, _ fstest.MapFS) { d.Journal.Step = 1 },
		"scope":      func(d *referenceDocument, _ fstest.MapFS) { d.Scope = "fully_qualified_product" },
		"seed":       func(d *referenceDocument, _ fstest.MapFS) { d.Steps[14].Kind = createTable },
	} {
		t.Run(name, func(t *testing.T) {
			document, files := fixtureCatalog(t)
			change(&document, files)
			raw, e := json.Marshal(document)
			if e != nil {
				t.Fatal(e)
			}
			if _, e := loadCatalog(raw, files); e == nil || e.Error() != CodeCatalogInvalid {
				t.Fatal("unsafe migration catalog admitted", e)
			}
		})
	}
}

func TestMySQLMigrationCatalogRejectsAmbiguousJSON(t *testing.T) {
	document, files := fixtureCatalog(t)
	raw, e := json.Marshal(document)
	if e != nil {
		t.Fatal(e)
	}
	for index, invalid := range [][]byte{nil, []byte("null"), append([]byte(`{"format":"duplicate",`), raw[1:]...), append(raw[:len(raw)-1:len(raw)-1], []byte(`,"unknown":true}`)...), []byte(strings.Repeat(" ", maximumCatalogBytes+1))} {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			if _, e := loadCatalog(invalid, files); e == nil {
				t.Fatal("ambiguous metadata accepted")
			}
		})
	}
}

// The isolated fixture capacity covers the current265-step catalog. Lower
// explicit limits remain covered by rejection cases; runtime policy is unchanged.
const migrationFixtureMaximumSteps = 265

func testOptions() Options { return Options{1, 5, 60, 3, migrationFixtureMaximumSteps, 65536} }

func TestMySQLMigrationOptionsAndDatabaseScopedLock(t *testing.T) {
	valid := testOptions()
	if valid.Validate() != nil {
		t.Fatal("explicit bounded options")
	}
	for _, invalid := range []Options{{}, {0, 5, 60, 3, 64, 65536}, {1, 301, 600, 3, 64, 65536}, {5, 5, 9, 3, 64, 65536}, {1, 5, 60, 0, 64, 65536}, {1, 5, 60, 3, 0, 65536}, {1, 5, 60, 3, 64, 1048577}} {
		if invalid.Validate() == nil {
			t.Fatal("invalid operator budget admitted")
		}
	}
	first, e := LockName("scope_one_test")
	second, f := LockName("scope_two_test")
	if e != nil || f != nil || first == second || len(first) > 64 {
		t.Fatal("server-wide lock scope")
	}
	for _, database := range []string{"", "../other", "name`", "UPPER", strings.Repeat("x", 65)} {
		if _, e := LockName(database); e == nil {
			t.Fatal("unsafe lock scope")
		}
	}
}
