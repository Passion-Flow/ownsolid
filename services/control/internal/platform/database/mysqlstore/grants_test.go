//go:build mysql_integration

package mysqlstore

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

func grantNeighbor(t *testing.T, c Configuration) Configuration {
	t.Helper()
	neighbor := os.Getenv("PROJECT_ONE_MYSQL_GRANT_NEIGHBOR")
	if !databaseName.MatchString(neighbor) || neighbor != strings.Replace(c.Database, "_", "x", 1) || !strings.HasSuffix(neighbor, "_test") {
		t.Fatal("MYSQL_GRANT_OWNED_SYNTHETIC_NEIGHBOR_REQUIRED")
	}
	c.Database = neighbor
	return c
}

func TestActualMySQLFixtureLegacyGrantWitness(t *testing.T) {
	_, c := actualStore(t)
	neighbor := grantNeighbor(t, c)
	candidate, observed, e := Open(context.Background(), neighbor)
	if e != nil {
		t.Fatal("declared legacy witness did not authenticate", e)
	}
	defer candidate.Close()
	var marker string
	if e = candidate.Pool().QueryRowContext(context.Background(), "SELECT marker FROM boundary_probe WHERE id=1").Scan(&marker); e != nil || marker != "synthetic-grant-boundary" || !observed.TLSAuthenticated {
		t.Fatal("actual synthetic legacy witness", SafeError(e))
	}
}

func TestActualMySQLFixtureLiteralGrantRejectsNeighbor(t *testing.T) {
	s, c := actualStore(t)
	neighbor := grantNeighbor(t, c)
	candidate, _, e := Open(context.Background(), neighbor)
	if candidate != nil {
		candidate.Close()
	}
	var safe *Error
	if e == nil || !errors.As(e, &safe) || safe.Number != 1044 {
		t.Fatal("actual neighboring database not denied by database privilege", e)
	}
	var count int
	if e = s.Pool().QueryRowContext(context.Background(), "SELECT COUNT(*) FROM kernel_parent").Scan(&count); e != nil || count != 5 {
		t.Fatal("intended retained synthetic data unavailable", SafeError(e))
	}
}
