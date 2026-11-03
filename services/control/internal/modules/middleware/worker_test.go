package middleware

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/deploy/offline/pkg/maintenance"
)

func TestPrivateActorSnapshotRetainsOnlyRequiredCurrentAuthority(t *testing.T) {
	a := identity.Auth{}
	a.Me.User.ID = "11111111-1111-1111-1111-111111111111"
	a.Me.User.Email = "private@example.invalid"
	a.Me.Audience = "portal"
	a.SessionID = "22222222-2222-2222-2222-222222222222"
	a.TokenHash = []byte(strings.Repeat("x", 32))
	a.Epoch = 7
	saved := snapshot(a)
	raw, _ := json.Marshal(saved)
	restored := saved.auth()
	if strings.Contains(string(raw), "private@example.invalid") || !actor(restored) || restored.Epoch != 7 || restored.SessionID != a.SessionID {
		t.Fatal("snapshot lost authority or retained unrelated private data")
	}
	a.TokenHash[0] = 'y'
	if saved.TokenHash[0] == 'y' {
		t.Fatal("snapshot shared mutable credential hash")
	}
}

func TestOriginalOperationReasonBindsNativeOwnerIntent(t *testing.T) {
	first, e := boundReason("11111111-1111-1111-1111-111111111111", "Synthetic reason")
	if e != nil {
		t.Fatal(e)
	}
	second, e := boundReason("22222222-2222-2222-2222-222222222222", "Synthetic reason")
	if e != nil || first == second {
		t.Fatal("different control intent could adopt original owner request")
	}
	if _, e = boundReason("11111111-1111-1111-1111-111111111111", strings.Repeat("\u6587", 500)); e == nil {
		t.Fatal("native reason limit silently exceeded")
	}
}

// Synthetic metadata exercises receipt selection only. It does not qualify a
// backup artifact, SQL restore, M17 scope, role or cryptographic proof producer.
func TestReconciledBackupKeepsOriginalArchiveNamespace(t *testing.T) {
	scope := strings.Repeat("a", 32)
	digest := strings.Repeat("b", 64)
	stages := []maintenance.Result{{Command: maintenance.ReconcileBackup, Status: "complete_selected_profile_backup_restore_pending", ScopeID: scope, ManifestSHA256: digest, EvidenceSHA256: digest}, {Command: maintenance.InspectRestoreSource, Status: "verified_independent_backup_source_not_restored", SourceScopeID: scope, ManifestSHA256: digest, PlanSHA256: digest, EvidenceSHA256: digest, Source: &maintenance.DatabaseSource{Database: "synthetic", Operator: "synthetic", ServerVersionNumber: 180006, TenantID: "11111111-1111-1111-1111-111111111111", SchemaVersion: 16, VectorVersion: "synthetic"}}}
	original := "22222222-2222-2222-2222-222222222222"
	raw, _ := json.Marshal(executionEvidence{FormatVersion: 1, Kind: "backup", Stages: stages, OriginalOperationID: original})
	op := Operation{ID: "33333333-3333-3333-3333-333333333333", Kind: "reconcile", State: "succeeded", Result: raw}
	_, root, e := backupReceipt(op)
	if e != nil || root != original {
		t.Fatal("reconciliation relocated original archive", e)
	}
	for _, command := range []maintenance.Command{maintenance.Backup, maintenance.InspectBackup, maintenance.ReconcileBackup} {
		stages[0].Command = command
		op.Result = mustJSON(executionEvidence{FormatVersion: 1, Kind: "backup", Stages: stages, OriginalOperationID: original})
		if _, _, e := backupReceipt(op); e != nil {
			t.Fatalf("completed original command %s refused: %v", command, e)
		}
	}
	for _, test := range []struct {
		name   string
		mutate func([]maintenance.Result)
	}{
		{"pending_source", func(s []maintenance.Result) { s[1].Pending = true }},
		{"wrong_backup_command", func(s []maintenance.Result) { s[0].Command = maintenance.RestoreSQL }},
		{"wrong_source_command", func(s []maintenance.Result) { s[1].Command = maintenance.InspectRestoreTarget }},
		{"incomplete_source", func(s []maintenance.Result) { s[1].Status = "restore_reservation_outcome_unresolved" }},
		{"missing_source_plan", func(s []maintenance.Result) { s[1].PlanSHA256 = "" }},
		{"changed_manifest", func(s []maintenance.Result) { s[1].ManifestSHA256 = strings.Repeat("c", 64) }},
		{"missing_source_evidence", func(s []maintenance.Result) { s[1].EvidenceSHA256 = "" }},
		{"invalid_source", func(s []maintenance.Result) { s[1].Source = &maintenance.DatabaseSource{} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := append([]maintenance.Result(nil), stages...)
			test.mutate(changed)
			op.Result = mustJSON(executionEvidence{FormatVersion: 1, Kind: "backup", Stages: changed, OriginalOperationID: original})
			if evidence, root, e := backupReceipt(op); e == nil || root != "" || len(evidence.Stages) != 0 {
				t.Fatal("invalid native stage accepted as completed backup")
			}
		})
	}
	stages[0].Pending = true
	op.Result = mustJSON(executionEvidence{FormatVersion: 1, Kind: "backup", Stages: stages, OriginalOperationID: original})
	if _, _, e = backupReceipt(op); e == nil {
		t.Fatal("pending original owner accepted as complete")
	}
	op.State = "unknown"
	if _, _, e = backupReceipt(op); e == nil {
		t.Fatal("unknown control outcome accepted as backup")
	}
}
