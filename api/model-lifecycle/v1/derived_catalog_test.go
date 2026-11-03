package v1

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// These are descriptor-only contract fixtures; no model file or approval is implied.
func derivedCatalogFixture() DerivedCatalog {
	const base = "11111111-1111-4111-8111-111111111111"
	const artifact = "22222222-2222-4222-8222-222222222222"
	files := ApprovedBaseFiles()
	for i := range files {
		if files[i].Path == "model.safetensors" {
			files[i].SizeBytes = 1192135096
			files[i].SHA256 = strings.Repeat("c", 64)
		}
	}
	return DerivedCatalog{SchemaVersion: 1, Kind: "merged_lora", Scope: Scope{TenantID: base, WorkspaceID: base, PreparationID: base}, ModelID: artifact, BaseModelID: base, BaseCatalogSHA256: ApprovedModelCatalogSHA256, AttemptID: base, ArtifactID: artifact, ReportSHA256: strings.Repeat("d", 64), RecipeSHA256: RecipeDigest(), BaseRepository: ApprovedBaseRepository, BaseRevision: ApprovedBaseRevision, License: ApprovedBaseLicense, Format: "safetensors", Architecture: ApprovedBaseArchitecture, ModelType: ApprovedBaseModelType, Precision: "BF16", MaximumContextTokens: MaximumContextTokens, Files: files}
}
func TestDerivedCatalogCanonicalHashPreservesExactLocalIdentityAndBaseProvenance(t *testing.T) {
	c := derivedCatalogFixture()
	raw, e := c.CanonicalBytes()
	if e != nil {
		t.Fatal(e)
	}
	decoded, e := DecodeDerivedCatalog(raw)
	if e != nil {
		t.Fatal(e)
	}
	sha, e := c.Digest()
	if e != nil || len(sha) != 64 {
		t.Fatal(e)
	}
	actual, e := decoded.Digest()
	if e != nil || actual != sha {
		t.Fatal("canonical round trip", e)
	}
	if !bytes.HasPrefix(raw, []byte(`{"architecture":`)) || !bytes.Contains(raw, []byte(`"scope":{"preparation_id":`)) {
		t.Fatal("object keys were not recursively sorted")
	}
	var pretty bytes.Buffer
	if json.Indent(&pretty, raw, "", "  ") != nil {
		t.Fatal("pretty catalog")
	}
	spaced, e := DecodeDerivedCatalog(pretty.Bytes())
	if e != nil {
		t.Fatal(e)
	}
	actual, e = spaced.Digest()
	if e != nil || actual != sha {
		t.Fatal("equivalent JSON whitespace changed typed identity")
	}
	c.ReportSHA256 = strings.Repeat("e", 64)
	changed, e := c.Digest()
	if e != nil || changed == sha {
		t.Fatal("report lineage not hashed")
	}
}
func TestDerivedCatalogRejectsForeignIdentityPrecisionBaseFilesUnknownAndAmbiguousJSON(t *testing.T) {
	changes := map[string]func(*DerivedCatalog){
		"old_schema":         func(c *DerivedCatalog) { c.SchemaVersion = 2 },
		"wrong_kind":         func(c *DerivedCatalog) { c.Kind = "approved_base" },
		"foreign_scope":      func(c *DerivedCatalog) { c.Scope.WorkspaceID = "../foreign" },
		"different_model":    func(c *DerivedCatalog) { c.ModelID = c.BaseModelID },
		"different_artifact": func(c *DerivedCatalog) { c.ArtifactID = c.BaseModelID },
		"base_precision":     func(c *DerivedCatalog) { c.Precision = "FP16" },
		"context":            func(c *DerivedCatalog) { c.MaximumContextTokens++ },
		"architecture":       func(c *DerivedCatalog) { c.Architecture = "RemoteCodeModel" },
		"remote_revision":    func(c *DerivedCatalog) { c.BaseRevision = strings.Repeat("a", 40) },
		"license":            func(c *DerivedCatalog) { c.License = "invented" },
		"report":             func(c *DerivedCatalog) { c.ReportSHA256 = "invalid" },
		"recipe":             func(c *DerivedCatalog) { c.RecipeSHA256 = strings.Repeat("a", 64) },
		"missing_file":       func(c *DerivedCatalog) { c.Files = c.Files[:len(c.Files)-1] },
		"duplicate_file":     func(c *DerivedCatalog) { c.Files[1] = c.Files[0] },
		"out_of_order":       func(c *DerivedCatalog) { c.Files[0], c.Files[1] = c.Files[1], c.Files[0] },
		"tampered_license":   func(c *DerivedCatalog) { c.Files[0].SHA256 = strings.Repeat("a", 64) },
		"unchanged_base":     func(c *DerivedCatalog) { c.Files = ApprovedBaseFiles() },
		"budget":             func(c *DerivedCatalog) { c.Files[5].SizeBytes = MaximumModelBytes },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			c := derivedCatalogFixture()
			change(&c)
			if c.Validate() == nil {
				t.Fatal("invalid derivative accepted")
			}
		})
	}
	c := derivedCatalogFixture()
	raw, _ := json.Marshal(c)
	for _, bad := range [][]byte{bytes.Replace(raw, []byte(`"schema_version":1`), []byte(`"schema_version":1.0`), 1), bytes.Replace(raw, []byte(`"schema_version":1`), []byte(`"schema_version":null`), 1), bytes.Replace(raw, []byte(`"schema_version":1,`), nil, 1), append([]byte(`{"unknown":1,`), raw[1:]...), append([]byte(`{"schema_version":1,`), raw[1:]...)} {
		if _, e := DecodeDerivedCatalog(bad); e == nil {
			t.Fatal("ambiguous/unknown catalog decoded")
		}
	}
}
