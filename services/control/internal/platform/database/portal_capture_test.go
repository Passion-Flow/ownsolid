package database_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/Passion-Flow/ownsolid/services/control/migrations"
	"testing"
)

func TestM15CatalogCapture(t *testing.T) {
	s := fixture.NewM14(t)
	var old string
	if e := s.DB.QueryRow(t.Context(), database.PluginCatalogSQL).Scan(&old); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(os.Getenv("PROJECT_ONE_EVIDENCE_ROOT"), "plugin-catalog-predecessor.json"), []byte(old), 0600); e != nil {
		t.Fatal(e)
	}
	raw, e := migrations.Files.ReadFile("account/000021_portal.up.sql")
	if e != nil {
		t.Fatal(e)
	}
	tx, e := s.DB.Begin(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(t.Context())
	if _, e = tx.Exec(t.Context(), string(raw)); e != nil {
		t.Fatal(e)
	}
	sum := sha256.Sum256(raw)
	if _, e = tx.Exec(t.Context(), "INSERT INTO schema_migrations(version,checksum) VALUES(21,$1)", hex.EncodeToString(sum[:])); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(t.Context()); e != nil {
		t.Fatal(e)
	}
	var inventory string
	if e = s.DB.QueryRow(t.Context(), database.PortalCatalogSQL).Scan(&inventory); e != nil {
		t.Fatal(e)
	}
	h := sha256.Sum256([]byte(inventory))
	t.Log("CATALOG_SHA256", hex.EncodeToString(h[:]))
	root := os.Getenv("PROJECT_ONE_EVIDENCE_ROOT")
	if root == "" {
		t.Fatal("evidence root required")
	}
	if e = os.WriteFile(filepath.Join(root, "portal-catalog-fresh.json"), []byte(inventory), 0600); e != nil {
		t.Fatal(e)
	}
	owned := fixture.OwnedPostgres(t, s.DB)
	archive := owned.DumpSchema(t, t.Context())
	restored := owned.RestoreFresh(t, t.Context(), s.DB, archive, true)
	var restoredInventory string
	if e = restored.QueryRow(t.Context(), database.PortalCatalogSQL).Scan(&restoredInventory); e != nil {
		t.Fatal(e)
	}
	rh := sha256.Sum256([]byte(restoredInventory))
	t.Log("RESTORED_CATALOG_SHA256", hex.EncodeToString(rh[:]))
	if e = os.WriteFile(filepath.Join(root, "portal-catalog-restored.json"), []byte(restoredInventory), 0600); e != nil {
		t.Fatal(e)
	}

}
