package database

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/migrations"
)

// The migration integrator can assign a lineage number, but cannot silently
// fork the reviewed domain's membership and populated rollback rules.
func TestModelFamilyMigrationPreservesReviewedOwnerSQL(t *testing.T) {
	for _, item := range []struct{ name, digest string }{
		{"000061_model_families.up.sql", "072172f08a85b62b4b9c10ce6c8e5cbdfa7f88728522472bc5171f034b54e374"},
		{"000061_model_families.down.sql", "0dc0c6c0c2821a0eead8399db277ab5de2538bf4b2b6ff6b3b8b73dcd8159cb6"},
	} {
		t.Run(item.name, func(t *testing.T) {
			raw, err := migrations.Files.ReadFile("postgresql/" + item.name)
			if err != nil {
				t.Fatal(err)
			}
			_, body, header := bytes.Cut(raw, []byte("\n"))
			domain, _, platform := bytes.Cut(body, []byte("\n-- Platform cell ownership.\n"))
			if !header || !platform || fmt.Sprintf("%x", sha256.Sum256(domain)) != item.digest {
				t.Fatal("formal migration changed the reviewed MODEL-owned SQL")
			}
		})
	}
}
