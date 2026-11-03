//go:build repository_integration && postgres_backup_integration

package postgresbackup

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// The actual restore CLI has joined and released its writer session. This
// independent observer keeps one original target pool/lease, creates no role
// and calls the production readonly owner before the later preparation stage.
func assertActualMVPRestoreReference(t *testing.T, ctx context.Context, r release.PostgreSQLRestoreRequest, operator, runtime config.Config, operatorPath, requestPath string, present bool) {
	t.Helper()
	raw, e := readPrivateBytes(operator.Database.DSNFile, 16<<10)
	if e != nil {
		t.Fatal("protected target observer connection")
	}
	pc, e := pgxpool.ParseConfig(strings.TrimSpace(string(raw)))
	if e != nil || pc.ConnConfig.Host != "127.0.0.1" || pc.ConnConfig.Port < 44900 || pc.ConnConfig.Port > 44999 || pc.ConnConfig.Database != r.TargetDatabase || pc.ConnConfig.User != r.TargetOperator {
		t.Fatal("exact target observer binding required")
	}
	pc.MaxConns = 2
	pc.MinConns = 0
	pc.ConnConfig.RuntimeParams["search_path"] = "public"
	pool, e := pgxpool.NewWithConfig(ctx, pc)
	if e != nil {
		t.Fatal("target observer pool")
	}
	defer pool.Close()
	lease, e := pool.Acquire(ctx)
	if e != nil {
		t.Fatal("target observer lease")
	}
	var held bool
	if lease.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", database.InstallationLockID).Scan(&held) != nil || !held {
		lease.Release()
		t.Fatal("original target observer lock")
	}
	defer func() {
		clean, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var released bool
		if lease.QueryRow(clean, "SELECT pg_advisory_unlock($1)", database.InstallationLockID).Scan(&released) != nil || !released {
			t.Error("target observer release unconfirmed")
		}
		lease.Release()
	}()
	out, e := ObserveRestore(ctx, pool, lease, operator, runtime, operatorPath, "", requestPath, "", r)
	if e != nil || out.OriginalCompletionPresent != present || !out.ReadOnly || out.CurrentLockHeld {
		t.Fatal("original native restore observation", e)
	}
	label := "missing-completion"
	if present {
		label = "complete"
		if out.NativePostgreSQL == nil || !out.NativePostgreSQL.Valid() || out.NativePostgreSQL.ManifestSHA256 != r.SourceManifestSHA256 || out.NativePostgreSQL.Registry == nil {
			t.Fatal("complete actual restore safe reference missing")
		}
	} else if out.NativePostgreSQL != nil {
		t.Fatal("absent completion manufactured a completed restore reference")
	}
	if e = fixtureJSON(filepath.Join("/fixture", "observer-native-restore-"+label+".json"), out); e != nil {
		t.Fatal("safe native restore observation receipt", e)
	}
}
