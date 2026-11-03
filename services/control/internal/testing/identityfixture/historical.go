package identityfixture

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/migrations"
	"strings"
	"testing"
)

// NewAtVersion creates only a new owned synthetic schema at the requested
// historical migration. It never rolls newer tables back in existing state.
// Product upgrades remain exercised through the actual database.Migrate path.
func NewAtVersion(t *testing.T, version int) *identity.Service {
	t.Helper()
	if version < 1 {
		t.Fatal("HISTORICAL_FIXTURE_VERSION_REQUIRED")
	}
	s := NewM01(t)
	entries, e := fs.ReadDir(migrations.Files, "postgresql")
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	last := 1
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".up.sql") {
			continue
		}
		var selected int
		if _, e = fmt.Sscanf(entry.Name(), "%d_", &selected); e != nil {
			t.Fatal(e)
		}
		if selected <= 1 || selected > version {
			continue
		}
		if selected != last+1 {
			t.Fatal("HISTORICAL_FIXTURE_MIGRATION_GAP")
		}
		raw, e := migrations.Files.ReadFile("postgresql/" + entry.Name())
		if e != nil {
			t.Fatal(e)
		}
		if _, e = tx.Exec(ctx, string(raw)); e != nil {
			t.Fatal(e)
		}
		sum := sha256.Sum256(raw)
		if _, e = tx.Exec(ctx, "INSERT INTO schema_migrations(version,checksum) VALUES($1,$2)", selected, hex.EncodeToString(sum[:])); e != nil {
			t.Fatal(e)
		}
		last = selected
	}
	if last != version {
		t.Fatal("HISTORICAL_FIXTURE_VERSION_NOT_FOUND")
	}
	if e = tx.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	return s
}
