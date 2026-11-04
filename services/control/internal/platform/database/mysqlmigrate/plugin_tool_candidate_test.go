package mysqlmigrate

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	base "github.com/Passion-Flow/ownsolid/services/control/migrations/mysql/candidates/knowledge-workflow-portal"
	candidate "github.com/Passion-Flow/ownsolid/services/control/migrations/mysql/candidates/plugin-tool"
)

type pluginCandidateDocument struct {
	Format           string             `json:"format"`
	Status           string             `json:"status"`
	Base             int                `json:"base_step_count"`
	Start            int                `json:"start_step"`
	End              int                `json:"end_step"`
	BaseSHA256       string             `json:"base_candidate_catalog_sha256"`
	PostgreSQLSHA256 string             `json:"postgresql_source_sha256"`
	Steps            []kwpCandidateStep `json:"steps"`
	Blockers         []string           `json:"activation_blockers"`
}

func pluginCandidateLoad(t *testing.T) pluginCandidateDocument {
	t.Helper()
	var d pluginCandidateDocument
	raw, e := candidate.Files.ReadFile("catalog.json")
	if e != nil || json.Unmarshal(raw, &d) != nil || d.Format != "projectone.mysql.plugin-tool-schema-source-candidate.v1" || d.Status != "candidate_source_only_not_measured_not_activated" || d.Base != 183 || d.Start != 184 || d.End != 194 || len(d.Steps) != 11 || len(d.Blockers) != 4 {
		t.Fatal("source-only plugin candidate contract", e)
	}
	raw, e = base.Files.ReadFile("catalog.json")
	if e != nil || hash(string(raw)) != d.BaseSHA256 {
		t.Fatal("exact unexecuted base candidate source binding", e)
	}
	if _, e = kwpCandidateLoad(base.Files); e != nil {
		t.Fatal(e)
	}
	names, e := fs.Glob(candidate.Files, "*.sql")
	if e != nil || len(names) != 11 {
		t.Fatal("plugin candidate ownership")
	}
	seen := map[string]bool{}
	for index, item := range d.Steps {
		if item.Step != 184+index || item.MetadataStatus != "not_measured" || !identifier.MatchString(item.Object) || !strings.HasPrefix(item.File, fmt.Sprintf("%06d_", item.Step)) || filepath.Base(item.File) != item.File || seen[item.File] {
			t.Fatal("plugin candidate identity/order")
		}
		seen[item.File] = true
		order := 1
		if item.Step == 194 {
			order = 2
		}
		if item.Kind == "create_trigger" && item.ExpectedTriggerOrder != order || item.Kind != "create_trigger" && item.ExpectedTriggerOrder != 0 {
			t.Fatal("plugin candidate trigger order ownership")
		}
		raw, e = candidate.Files.ReadFile(item.File)
		if e != nil || hash(string(raw)) != item.SourceSHA256 || !strings.HasSuffix(string(raw), ";\n") {
			t.Fatal("plugin candidate source checksum", item.Step)
		}
		prefixes := map[string]string{"create_table": "CREATE TABLE " + item.Object + " (", "alter_table": "ALTER TABLE " + item.Object + "\n", "create_trigger": "CREATE TRIGGER " + item.Object + " "}
		if prefixes[item.Kind] == "" || !strings.HasPrefix(string(raw), prefixes[item.Kind]) {
			t.Fatal("plugin candidate explicit statement ownership")
		}
		for _, bad := range []string{"DEFERRABLE", "FOREIGN_KEY_CHECKS", "CREATE FUNCTION", "CREATE PROCEDURE", "DELIMITER", "DROP TABLE", "DROP TRIGGER"} {
			if strings.Contains(strings.ToUpper(string(raw)), bad) {
				t.Fatal("unsupported plugin candidate construct")
			}
		}
	}
	for _, name := range names {
		if !seen[name] {
			t.Fatal("unowned plugin source")
		}
	}
	return d
}
func TestMySQLPluginToolCandidateSource(t *testing.T) {
	d := pluginCandidateLoad(t)
	raw, e := os.ReadFile(filepath.Join("..", "testdata", "platform-lineage", "000014_plugins.up.sql"))
	if e != nil || hash(string(raw)) != d.PostgreSQLSHA256 {
		t.Fatal("frozen real PG14 source binding", e)
	}
	c, e := DefaultCatalog()
	if e != nil || c.Steps() != 265 {
		t.Fatal("current complete schema required beside historical plugin source", e)
	}
	raw, e = candidate.Files.ReadFile("000192_workflow_run_tool_result.sql")
	if e != nil || !strings.Contains(string(raw), "DROP CHECK workflow_non_success_result_absent") || !strings.Contains(string(raw), "DROP CHECK workflow_success_result_required") || strings.Contains(string(raw), "DROP CHECK workflow_answer_bytes") {
		t.Fatal("exact named result checks required")
	}
}
