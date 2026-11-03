package maintenance

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/scope"
)

func TestFixedOwnerCommandAndExactRequestUnion(t *testing.T) {
	b := BackupDefaults()
	for _, command := range []Command{"", "abort-backup", "shell", "backup --clean"} {
		if (Request{Backup: &b}).validate(command) == nil {
			t.Fatal("foreign command accepted", command)
		}
	}
	if (Request{Backup: &b, Restore: &RestoreRequest{}}).validate(Backup) == nil {
		t.Fatal("mixed request accepted")
	}
	if (Request{}).validate(Backup) == nil {
		t.Fatal("missing owning request accepted")
	}
}

func TestOriginalPendingEvidenceIsNotRelabeled(t *testing.T) {
	original := struct {
		Status        string `json:"status"`
		Pending       bool   `json:"pending"`
		Qualification string `json:"qualification"`
	}{"backup_incomplete_requires_reconciliation", true, "original_qualification"}
	r, e := project(Backup, original)
	if e != nil || !r.Pending || r.Status != original.Status || r.Qualification != original.Qualification || r.EvidenceSHA256 != digest(r.Original) {
		t.Fatal("original owner projection changed", e)
	}
	b, e := json.Marshal(r)
	if e != nil || strings.Contains(string(b), "Original") || strings.Contains(string(b), "original\":") {
		t.Fatal("private original exposed", e)
	}
	if _, e = project(Backup, struct{ Status string }{"success"}); e == nil {
		t.Fatal("missing original qualification accepted")
	}
}

func TestMissingOwnerAndUnboundedCallReturnZero(t *testing.T) {
	r, e := Run(context.Background(), nil, Backup, Request{})
	if e == nil || r.Status != "" || len(r.Original) != 0 {
		t.Fatal("missing owner returned output")
	}
	r, e = Run(context.Background(), &Configuration{fingerprint: "configured"}, Backup, Request{})
	if e == nil || e.Error() != "MAINTENANCE_OPERATION_DEADLINE_REQUIRED" || r.Status != "" {
		t.Fatal("unbounded owner call accepted")
	}
}

func TestRegisteredScopeCannotUseEmptyOrForeignIdentityType(t *testing.T) {
	for _, id := range []string{"", "11111111-1111-1111-1111-111111111111", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "a"} {
		c, e := LoadBound([]byte(`{}`), nil, id)
		if e == nil || e.Error() != "MAINTENANCE_REGISTERED_SCOPE_BINDING_REQUIRED" || c != nil {
			t.Fatal("unbound registered operation accepted", id, e)
		}
	}
}

func TestOriginalDeclaredStoresArePreservedInPublicEvidence(t *testing.T) {
	// Explicit synthetic unit projection only, never a completed native receipt.
	original := struct {
		Status        string   `json:"status"`
		Qualification string   `json:"qualification"`
		Stores        []string `json:"stores"`
	}{"verified_independent_backup_source_not_restored", "synthetic_projection_only", []string{"datasets", "models", "preparations", "training"}}
	r, e := project(InspectRestoreSource, original)
	if e != nil || len(r.Stores) != 4 || r.Stores[2] != "preparations" || r.EvidenceSHA256 != digest(r.Original) {
		t.Fatal("declared original stores lost", e)
	}
	original.Stores[0] = "changed"
	if r.Stores[0] != "datasets" {
		t.Fatal("mutable caller stores changed projected evidence")
	}
}

// Use the original owner type: this projection must not discard its selected
// signed release profile or turn an inventory observation into a restored target.
func TestOriginalRestoreSourceProfileSurvivesProjectionAndStoredReadback(t *testing.T) {
	for _, profile := range []string{"", "m17-postgresql-cpu-linux-amd64", "mvp-postgresql-cpu-v1-linux-amd64"} {
		t.Run(profile, func(t *testing.T) {
			original := scope.RestoreSourceResult{Status: "verified_independent_backup_source_not_restored", Profile: profile, Qualification: "readonly_inventory_only_not_restored"}
			r, err := project(InspectRestoreSource, original)
			if err != nil || r.Profile != profile || r.Status != original.Status || r.Pending || r.Qualification != original.Qualification || r.EvidenceSHA256 != digest(r.Original) {
				t.Fatal("original source profile or inventory qualification changed", err)
			}
			raw, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			var stored Result
			if json.Unmarshal(raw, &stored) != nil || stored.Profile != profile || stored.Status != original.Status || stored.Qualification != original.Qualification {
				t.Fatal("stored source profile or qualification lost")
			}
			if (profile == "") != !strings.Contains(string(raw), `"profile":`) {
				t.Fatal("absent legacy profile gained a new JSON field")
			}
			if strings.Contains(string(raw), "original\":") || strings.Contains(string(raw), "backup_root") {
				t.Fatal("private original source input exposed")
			}
		})
	}
}
