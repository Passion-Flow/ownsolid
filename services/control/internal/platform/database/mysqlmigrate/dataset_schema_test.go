package mysqlmigrate

import (
	"bytes"
	"strings"
	"testing"

	assets "github.com/Passion-Flow/ownsolid/services/control/migrations/mysql"
)

func TestMySQLDatasetSchemaImmutablePrefixAndOwnedGraph(t *testing.T) {
	c, e := DefaultCatalog()
	if e != nil || c.Steps() < 59 {
		t.Fatal("current initial dataset catalog", e)
	}
	raw, _ := assets.Files.ReadFile("evolution.json")
	cut := bytes.LastIndex(raw, []byte(",\n    {\n      \"step\": 52,"))
	if cut < 0 || hash(string(append(append([]byte{}, raw[:cut]...), []byte("\n  ]\n}\n")...))) != "e4eddaf331467633e1523edcc4e6fe5b84267f8781ec102ad2edd3d953914e9a" {
		t.Fatal("immutable1–51 serialized evolution prefix changed")
	}
	for n, name := range []string{"datasets", "dataset_drafts", "dataset_rows", "dataset_row_history", "dataset_jobs", "dataset_job_attempts", "dataset_versions"} {
		step := c.steps[51+n]
		if step.Step != 52+n || step.Object != name || step.Kind != createTable {
			t.Fatal("dataset dependency order/ownership changed")
		}
	}
	final := c.steps[58]
	if final.Kind != alterTable || final.Object != "dataset_drafts" || final.PreviousDefinitionSHA256 != c.steps[52].DefinitionSHA256 {
		t.Fatal("final dataset graph alteration lost measured draft predecessor")
	}
	for _, name := range []string{"dataset_draft_current_job_fk", "dataset_draft_published_version_fk", "dataset_draft_parent_version_fk"} {
		if !strings.Contains(final.sql, name) {
			t.Fatal("final dataset graph scoped reference omitted", name)
		}
	}
}
