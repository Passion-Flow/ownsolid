package mysqlmigrate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	assets "github.com/Passion-Flow/ownsolid/services/control/migrations/mysql"
	candidate "github.com/Passion-Flow/ownsolid/services/control/migrations/mysql/candidates/knowledge-workflow-portal"
)

type kwpCandidateStep struct {
	Step                 int    `json:"step"`
	File                 string `json:"file"`
	Object               string `json:"object"`
	Kind                 string `json:"kind"`
	SourceSHA256         string `json:"source_sha256"`
	MetadataStatus       string `json:"metadata_status"`
	ExpectedTriggerOrder int    `json:"expected_trigger_order,omitempty"`
}
type kwpCandidateDocument struct {
	Format           string             `json:"format"`
	Status           string             `json:"status"`
	Start            int                `json:"start_step"`
	End              int                `json:"end_step"`
	Base             int                `json:"base_step_count"`
	PrefixSHA256     string             `json:"prefix_manifest_sha256"`
	Steps            []kwpCandidateStep `json:"steps"`
	RequiredMetadata []string           `json:"required_measured_metadata"`
	Blockers         []string           `json:"activation_blockers"`
}

// This loader validates source ownership only. It cannot construct a production
// Catalog: the candidate contains no measured definitions or journal authority.
func kwpCandidateLoad(files fs.FS) (kwpCandidateDocument, error) {
	var d kwpCandidateDocument
	raw, e := fs.ReadFile(files, "catalog.json")
	if e != nil {
		return d, e
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&d) != nil || d.Format != "projectone.mysql.schema-source-candidate.v1" || d.Status != "candidate_source_only_not_measured_not_activated" || d.Base != 149 || d.Start != 150 || d.End != 183 || len(d.Steps) != 34 || len(d.RequiredMetadata) != 4 || len(d.Blockers) != 4 || len(d.PrefixSHA256) != 64 {
		return d, fmt.Errorf("invalid source-only candidate contract")
	}
	names, e := fs.Glob(files, "*.sql")
	if e != nil || len(names) != 34 {
		return d, fmt.Errorf("candidate source ownership mismatch")
	}
	seen := map[string]bool{}
	for index, item := range d.Steps {
		if item.Step != 150+index || !identifier.MatchString(item.Object) || !strings.HasPrefix(item.File, fmt.Sprintf("%06d_", item.Step)) || !strings.HasSuffix(item.File, ".sql") || filepath.Base(item.File) != item.File || seen[item.File] || item.MetadataStatus != "not_measured" {
			return d, fmt.Errorf("candidate order or identity mismatch")
		}
		seen[item.File] = true
		if item.Kind == "create_trigger" && item.ExpectedTriggerOrder != 1 || item.Kind != "create_trigger" && item.ExpectedTriggerOrder != 0 {
			return d, fmt.Errorf("candidate trigger order ownership mismatch")
		}
		raw, e = fs.ReadFile(files, item.File)
		if e != nil || hash(string(raw)) != item.SourceSHA256 || !strings.HasSuffix(string(raw), ";\n") {
			return d, fmt.Errorf("candidate source checksum mismatch")
		}
		prefix := ""
		switch item.Kind {
		case "create_table":
			prefix = "CREATE TABLE " + item.Object + " ("
		case "alter_table":
			prefix = "ALTER TABLE " + item.Object + "\n"
		case "create_trigger":
			prefix = "CREATE TRIGGER " + item.Object + " "
		case "seed":
			if item.Step == 176 && item.Object == "user_preferences_seed" {
				prefix = "INSERT INTO user_preferences(user_id) SELECT id FROM users;"
			}
		}
		if prefix == "" || !strings.HasPrefix(string(raw), prefix) {
			return d, fmt.Errorf("candidate statement kind mismatch")
		}
		for _, forbidden := range []string{"DEFERRABLE", "FOREIGN_KEY_CHECKS", "CREATE FUNCTION", "CREATE PROCEDURE", "DELIMITER", "public.vector", "DROP TABLE", "DROP TRIGGER"} {
			if strings.Contains(strings.ToUpper(string(raw)), strings.ToUpper(forbidden)) {
				return d, fmt.Errorf("candidate unsupported construct")
			}
		}
	}
	for _, name := range names {
		if !seen[name] {
			return d, fmt.Errorf("unowned candidate source")
		}
	}
	return d, nil
}

func TestMySQLKnowledgeWorkflowPortalCandidateSource(t *testing.T) {
	d, e := kwpCandidateLoad(candidate.Files)
	if e != nil {
		t.Fatal(e)
	}
	prefixPath := filepath.Join("testdata", "knowledge-workflow-portal-schema-prefix.json")
	raw, e := os.ReadFile(prefixPath)
	if e != nil || hash(string(raw)) != d.PrefixSHA256 {
		t.Fatal("frozen149-prefix binding", e)
	}
	var prefix struct {
		Format string            `json:"format"`
		Count  int               `json:"active_step_count"`
		Status string            `json:"status"`
		Files  map[string]string `json:"files"`
	}
	if json.Unmarshal(raw, &prefix) != nil || prefix.Count != 149 || len(prefix.Files) != 152 {
		t.Fatal("frozen149-prefix inventory")
	}
	for name, digest := range prefix.Files {
		raw, e := assets.Files.ReadFile(name)
		if name == "evolution.json" {
			raw, e = candidate.Files.ReadFile("prefix-evolution149.json")
		}
		if e != nil || hash(string(raw)) != digest {
			t.Fatal("immutable149 source/metadata prefix changed", name)
		}
	}
	c, e := DefaultCatalog()
	if e != nil || c.Steps() != 265 {
		t.Fatal("current measured complete schema required", e)
	}
	for _, item := range d.Steps {
		active := c.steps[item.Step-1]
		if active.File != item.File || active.SourceSHA256 != item.SourceSHA256 || active.Object != item.Object || string(active.Kind) != item.Kind {
			t.Fatal("activated source differs from retained original candidate", item.Step)
		}
	}
}

func TestMySQLKnowledgeWorkflowPortalCandidateRejectsSourceDrift(t *testing.T) {
	files := fstest.MapFS{}
	e := fs.WalkDir(candidate.Files, ".", func(path string, entry fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if !entry.IsDir() {
			raw, e := candidate.Files.ReadFile(path)
			if e != nil {
				return e
			}
			files[path] = &fstest.MapFile{Data: raw, Mode: 0444}
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	files["000150_knowledge_bases.sql"].Data = append(append([]byte{}, files["000150_knowledge_bases.sql"].Data...), []byte("SELECT 1;\n")...)
	if _, e = kwpCandidateLoad(files); e == nil {
		t.Fatal("changed candidate SQL admitted")
	}
}
