//go:build repository_integration

package training_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
)

// This cheap prerequisite runs before migrations or business leaves. The actual
// selected connector enforces TLS, authentication, pinned version and session
// invariants; a socket-ready initialization server is insufficient.
func TestParallelMySQLTrainingTLSSmoke(t *testing.T) {
	c, e := mysqlstore.Load(os.Getenv("PROJECT_ONE_REPOSITORY_MYSQL_CONFIGURATION"), "")
	if e != nil || os.Getenv("PROJECT_ONE_REPOSITORY_ENGINE") != "mysql" || c.Host != "127.0.0.1" || !strings.HasPrefix(c.Database, "m20_repository_") || !strings.HasSuffix(c.Database, "_test") {
		t.Fatal("EXACT_OWNED_MYSQL_TRAINING_FIXTURE_REQUIRED")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	store, observed, e := mysqlstore.Open(ctx, c)
	if e != nil {
		t.Fatal("selected TLS connector", e)
	}
	t.Cleanup(func() {
		if e := store.Close(); e != nil {
			t.Error("selected TLS pool close", e)
		}
	})
	if !observed.TLSAuthenticated || observed.Version != c.ExpectedVersion || observed.Charset != mysqlstore.ConnectionCharset || observed.Collation != mysqlstore.ConnectionCollation || observed.Isolation != mysqlstore.TransactionIsolation {
		t.Fatal("selected TLS/version/session invariants")
	}
	var database, zone string
	var now time.Time
	if e = store.Pool().QueryRowContext(ctx, "SELECT DATABASE(),@@session.time_zone,UTC_TIMESTAMP(6)").Scan(&database, &zone, &now); e != nil || database != c.Database || zone != "+00:00" || now.Location() != time.UTC {
		t.Fatal("selected actual database/UTC", e)
	}
}
