package licensing_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/Passion-Flow/ownsolid/services/control/migrations"
)

func TestLicenseRepositoryActualSchema16UpgradePreservesIdentityAndMigrationRepeat(t *testing.T) {
	owner := fixture.NewM01(t)
	fixture.Bootstrap(t, owner)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	entries, e := migrations.Files.ReadDir("postgresql")
	if e != nil {
		t.Fatal(e)
	}
	// Explicit historical test-only schema construction. Production always uses
	// the real ordered migration operator, never this version selector.
	expectedVersions := 0
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".up.sql") {
			continue
		}
		expectedVersions++
		prefix, _, _ := strings.Cut(entry.Name(), "_")
		version, e := strconv.Atoi(prefix)
		if e != nil {
			t.Fatal(e)
		}
		if version <= 1 || version >= 17 {
			continue
		}
		raw, e := migrations.Files.ReadFile("postgresql/" + entry.Name())
		if e != nil {
			t.Fatal(e)
		}
		sum := sha256.Sum256(raw)
		if _, e = owner.DB.Exec(ctx, string(raw)); e != nil {
			t.Fatal("historical fixture initialization", entry.Name(), e)
		}
		if _, e = owner.DB.Exec(ctx, "INSERT INTO schema_migrations(version,checksum) VALUES($1,$2)", version, hex.EncodeToString(sum[:])); e != nil {
			t.Fatal(e)
		}
	}
	var admin string
	if e = owner.DB.QueryRow(ctx, "SELECT primary_admin_user_id::text FROM installation_state WHERE id=1").Scan(&admin); e != nil {
		t.Fatal(e)
	}
	if e = database.Check(ctx, owner.DB); e == nil {
		t.Fatal("new binary accepted version16 schema")
	}
	if e = database.Migrate(ctx, owner.DB); e != nil {
		t.Fatal("actual additive licensing upgrade", e)
	}
	if e = database.Check(ctx, owner.DB); e != nil {
		t.Fatal(e)
	}
	var deployment, afterAdmin string
	var users, versions int
	if e = owner.DB.QueryRow(ctx, "SELECT deployment_id::text FROM platform_license_state WHERE id=1").Scan(&deployment); e != nil {
		t.Fatal(e)
	}
	if e = database.Migrate(ctx, owner.DB); e != nil {
		t.Fatal("actual completed migration repeat", e)
	}
	var repeated string
	if e = owner.DB.QueryRow(ctx, "SELECT deployment_id::text FROM platform_license_state WHERE id=1").Scan(&repeated); e != nil || repeated != deployment {
		t.Fatal("repeat regenerated license deployment identity", e)
	}
	if e = owner.DB.QueryRow(ctx, "SELECT primary_admin_user_id::text,(SELECT count(*) FROM users),(SELECT count(*) FROM schema_migrations) FROM installation_state WHERE id=1").Scan(&afterAdmin, &users, &versions); e != nil || afterAdmin != admin || users != 1 || versions != expectedVersions {
		t.Fatal("upgrade lost existing account/metadata", e)
	}
}
