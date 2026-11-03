package recovery_test

import (
	"fmt"
	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/recovery"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"testing"
)

func TestM09RestoreInvalidationRollsBackEveryEarlierOwner(t *testing.T) {
	s := fixture.NewM09(t)
	password := fixture.Bootstrap(t, s)
	ctx := t.Context()
	a := fixture.Login(t, s, "admin@example.invalid", password, "admin")
	portal := fixture.Login(t, s, "admin@example.invalid", password, "portal")
	w := &workspaces.Service{DB: s.DB, Identity: s}
	n := &nodes.Service{DB: s.DB, Identity: s, Workspaces: w}
	if _, e := n.Mint(ctx, a, a.Me.Workspace.ID, "Synthetic restore authority", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	data, e := (&datasets.Service{DB: s.DB, Identity: s, Workspaces: w}).Create(ctx, a, datasets.CreateRequest{WorkspaceID: a.Me.Workspace.ID, Name: "Atomic restore corpus", Origin: "Synthetic metadata only", License: "Synthetic CC0", RightsAsserted: true, Reason: "Test late-owner rollback"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(ctx, `INSERT INTO dataset_drafts(id,tenant_id,workspace_id,dataset_id,created_by,requested_audience,requested_epoch,source_object_id,source_name,source_sha256,expected_bytes,state,upload_attempt_id,upload_expires_at,creation_key,creation_fingerprint) VALUES($1,$2,$3,$4,$5,'admin',$6,$7,'incomplete.jsonl',repeat('a',64),1,'uploading',$8,clock_timestamp()+interval '1 minute',$9,$10)`, uuid.NewString(), a.Me.Workspace.TenantID, a.Me.Workspace.ID, data.ID, a.Me.User.ID, a.Epoch, uuid.NewString(), uuid.NewString(), uuid.NewString(), make([]byte, 32)); e != nil {
		t.Fatal(e)
	}
	snapshot := func() map[string]string {
		t.Helper()
		result := map[string]string{}
		for _, table := range []string{"users", "sessions", "invitations", "password_reset_tokens", "mail_deliveries", "audit_events", "node_join_tokens", "managed_nodes", "node_tasks", "model_imports", "deployments", "gpu_leases", "gateway_instances", "api_keys", "gateway_requests", "gateway_recoveries", "dataset_drafts", "dataset_jobs", "dataset_job_attempts", "training_jobs", "training_attempts", "training_uploads"} {
			var raw string
			if e := s.DB.QueryRow(ctx, "SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text)::text,'[]') FROM "+table+" t").Scan(&raw); e != nil {
				t.Fatal(e)
			}
			result[table] = raw
		}
		return result
	}
	before := snapshot()
	if _, e = s.DB.Exec(ctx, `CREATE FUNCTION reject_training_restore() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic late training owner failure'; RETURN NULL; END $$; CREATE TRIGGER reject_training_restore BEFORE UPDATE ON training_jobs FOR EACH STATEMENT EXECUTE FUNCTION reject_training_restore()`); e != nil {
		t.Fatal(e)
	}
	if recovery.InvalidateRestored(ctx, s, "Synthetic atomic restore refusal") == nil {
		t.Fatal("late owner fault accepted")
	}
	for table, raw := range snapshot() {
		if raw != before[table] {
			t.Fatal("partial earlier-owner invalidation", table)
		}
	}
	if _, e = s.DB.Exec(ctx, `DROP TRIGGER reject_training_restore ON training_jobs; DROP FUNCTION reject_training_restore()`); e != nil {
		t.Fatal(e)
	}
	if e = recovery.InvalidateRestored(ctx, s, "Synthetic explicit restored-auth invalidation"); e != nil {
		t.Fatal(e)
	}
	var active int
	if e = s.DB.QueryRow(ctx, `SELECT (SELECT count(*) FROM sessions WHERE revoked_at IS NULL)+(SELECT count(*) FROM node_join_tokens WHERE consumed_at IS NULL AND revoked_at IS NULL)+(SELECT count(*) FROM dataset_drafts WHERE upload_attempt_id IS NOT NULL OR upload_expires_at IS NOT NULL)`).Scan(&active); e != nil || active != 0 {
		t.Fatal(fmt.Sprintf("retained authority=%d", active), e)
	}
	if _, e = w.Get(ctx, portal, a.Me.Workspace.ID, false); e == nil {
		t.Fatal("old account epoch regained authority")
	}
	fresh := fixture.Login(t, s, "admin@example.invalid", password, "portal")
	if _, e = w.Get(ctx, fresh, a.Me.Workspace.ID, false); e != nil {
		t.Fatal("business scope lost", e)
	}
}
