package database_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"testing"
)

func TestM14CatalogCapture(t *testing.T) {
	s := fixture.NewM13(t)
	var old string
	if e := s.DB.QueryRow(t.Context(), database.WorkflowCatalogSQL).Scan(&old); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(os.Getenv("PROJECT_ONE_EVIDENCE_ROOT"), "workflow-catalog-predecessor.json"), []byte(old), 0600); e != nil {
		t.Fatal(e)
	}
	if e := database.Migrate(t.Context(), s.DB, "plugins"); e != nil {
		t.Fatal(e)
	}
	var e error
	var inventory string
	if e = s.DB.QueryRow(t.Context(), database.PluginCatalogSQL).Scan(&inventory); e != nil {
		t.Fatal(e)
	}
	h := sha256.Sum256([]byte(inventory))
	t.Log("CATALOG_SHA256", hex.EncodeToString(h[:]))
	root := os.Getenv("PROJECT_ONE_EVIDENCE_ROOT")
	if root == "" {
		t.Fatal("evidence root required")
	}
	if e = os.WriteFile(filepath.Join(root, "plugin-catalog-fresh.json"), []byte(inventory), 0600); e != nil {
		t.Fatal(e)
	}
	owned := fixture.OwnedPostgres(t, s.DB)
	archive := owned.DumpSchema(t, t.Context())
	restored := owned.RestoreFresh(t, t.Context(), s.DB, archive, true)
	var restoredInventory string
	if e = restored.QueryRow(t.Context(), database.PluginCatalogSQL).Scan(&restoredInventory); e != nil {
		t.Fatal(e)
	}
	if e = database.Check(t.Context(), restored, "plugins"); e != nil {
		t.Fatal("restored cumulative catalog", e)
	}
	rh := sha256.Sum256([]byte(restoredInventory))
	t.Log("RESTORED_CATALOG_SHA256", hex.EncodeToString(rh[:]))
	if e = os.WriteFile(filepath.Join(root, "plugin-catalog-restored.json"), []byte(restoredInventory), 0600); e != nil {
		t.Fatal(e)
	}

}
