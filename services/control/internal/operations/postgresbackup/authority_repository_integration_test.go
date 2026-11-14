//go:build repository_integration && postgres_backup_integration

package postgresbackup

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

type retainedOriginalAuthority struct {
	Audience  string `json:"audience"`
	Token     string `json:"token"`
	SessionID string `json:"session_id"`
	UserID    string `json:"user_id"`
	Epoch     int64  `json:"epoch"`
	ExpiresAt string `json:"expires_at"`
}

// This necessary small full-schema profile preserves exact pre-backup tokens
// for subsequent real HTTP assertions. It does not rerun the populated fixture.
func TestActualPostgresOriginalAuthorityNativeCustodyAndFaults(t *testing.T) {
	f := newActualPostgresBackupFixture(t, 20*time.Minute, 600, 1<<30, 100, 16<<20)
	raw, e := canonical(f.request)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile("/fixture/request-native.json", raw, 0600); e != nil {
		t.Fatal(e)
	}
	f.close()
	actualPostgresRestoreWithAuthorityCustody(t, 20*time.Minute, false, true)
	for _, audience := range []string{"portal", "admin"} {
		var authority retainedOriginalAuthority
		if _, e = readPrivateDocument(filepath.Join("/fixture", "original-authority-"+audience+"-private.json"), 16<<10, &authority); e != nil || authority.Audience != audience || authority.Token == "" || authority.SessionID == "" || authority.Epoch != 1 {
			t.Fatal("exact original private credential retained", e)
		}
	}
	if e = fixtureJSON("/fixture/actual-authority-custody-receipt.json", map[string]any{"status": "passed_exact_original_private_tokens_before_matched_backup", "scope": "actual PG18.6 full releases22/86, initialized empty domain stores; original token custody plus existing native loss/root/replay assertions; served HTTP is a separate next continuation on this exact restored target", "no_populated_replay": true}); e != nil {
		t.Fatal(e)
	}
}
