//go:build gateway_recovery_integration

package distribution_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/recovery"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

func TestActualLogicalDistributionBackupRestorePreservesUnknownHistoryAndInvalidatesAuthority(t *testing.T) {
	c := setup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	fixture := identityfixture.OwnedPostgres(t, c.s.DB)
	var e error
	a := c.access(t)
	completed := a.input()
	if _, e = c.s.Admit(ctx, a.gateway.Gateway.ID, *a.gateway.Secret, completed, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	if _, e = c.s.Report(ctx, a.gateway.Gateway.ID, *a.gateway.Secret, success(completed.RequestID), uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	pending := a.input()
	if _, e = c.s.Admit(ctx, a.gateway.Gateway.ID, *a.gateway.Secret, pending, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	if _, e = c.s.RevokeGateway(ctx, c.a, a.alias.WorkspaceID, a.gateway.Gateway.ID, "Synthetic restore preparation", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	challenge, e := c.s.CreateRecoveryChallenge(ctx, c.a, a.alias.WorkspaceID, pending.RequestID, "Synthetic pending restore challenge", uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	archive := fixture.DumpSchema(t, ctx)
	output := os.Getenv("PROJECT_ONE_EVIDENCE_ROOT")
	if output == "" {
		t.Fatal("explicit private evidence root required")
	}
	if e = os.WriteFile(filepath.Join(output, "distribution-backup.dump"), archive, 0600); e != nil {
		t.Fatal(e)
	}
	restored := fixture.RestoreFresh(t, ctx, c.s.DB, archive, true)
	var held, reported, challenged int
	if e = restored.QueryRow(ctx, "SELECT count(*) FILTER(WHERE capacity_held),count(*) FILTER(WHERE outcome IS NOT NULL),count(*) FILTER(WHERE active_recovery_id IS NOT NULL) FROM gateway_requests").Scan(&held, &reported, &challenged); e != nil || held != 1 || reported != 1 || challenged != 1 {
		t.Fatal("actual dump/restore lost history or cyclic recovery references", e)
	}
	// The restored database is deliberately fenced before serving or accepting any old authority.
	original := c.s.Identity.DB
	c.s.Identity.DB = restored
	defer func() { c.s.Identity.DB = original }()
	if e = recovery.InvalidateRestored(ctx, c.s.Identity, "Actual isolated M07 logical restore"); e != nil {
		t.Fatal(e)
	}
	var invalidated, recoveryInvalidated, unknown bool
	if e = restored.QueryRow(ctx, "SELECT credential_hash IS NULL AND authority_invalidated_at IS NOT NULL FROM gateway_instances WHERE id=$1", a.gateway.Gateway.ID).Scan(&invalidated); e != nil || !invalidated {
		t.Fatal("restored gateway authority survived", e)
	}
	if e = restored.QueryRow(ctx, "SELECT invalidated_at IS NOT NULL FROM gateway_recoveries WHERE id=$1", challenge.ID).Scan(&recoveryInvalidated); e != nil || !recoveryInvalidated {
		t.Fatal("restored challenge survived", e)
	}
	if e = restored.QueryRow(ctx, "SELECT capacity_held AND outcome IS NULL FROM gateway_requests WHERE id=$1", pending.RequestID).Scan(&unknown); e != nil || !unknown {
		t.Fatal("restore fabricated terminal usage or free capacity", e)
	}
	var actual int
	if e = restored.QueryRow(ctx, "SELECT (outcome->'usage'->>'total_tokens')::integer FROM gateway_requests WHERE id=$1", completed.RequestID).Scan(&actual); e != nil || actual != 9 {
		t.Fatal("restored actual receipt metadata changed", e)
	}
	metadata := map[string]any{"status": "passed_actual_isolated_logical_restore", "unknown_capacity_retained": unknown, "gateway_authority_invalidated": invalidated, "pending_recovery_invalidated": recoveryInvalidated, "completed_usage_preserved": actual, "operator_and_node_evidence": "Explicitly synthetic service fixtures; real PostgreSQL dump/restore and invalidation. No model generation/GPU."}
	raw, _ := json.MarshalIndent(metadata, "", "  ")
	if e = os.WriteFile(filepath.Join(output, "distribution-restore.json"), append(raw, '\n'), 0600); e != nil {
		t.Fatal(e)
	}
}
