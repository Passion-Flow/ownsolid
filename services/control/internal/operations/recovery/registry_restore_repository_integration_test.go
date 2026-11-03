//go:build repository_integration

package recovery_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/recovery"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	lifecyclewire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

// Real complete platform catalog, constrained synthetic metadata and the
// original full restore coordinator. No image pull or physical execution is
// claimed; registry references and uncertain GPU custody must remain intact.
func TestActualFullRestoreRegistryGrantAndLastAuditAtomicity(t *testing.T) {
	s := fixture.New(t)
	password := fixture.Bootstrap(t, s)
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	a := fixture.Login(t, s, "admin@example.invalid", password, "admin")
	n := &nodes.Service{DB: s.DB, Identity: s, Workspaces: &workspaces.Service{DB: s.DB, Identity: s}}
	token, e := n.Mint(ctx, a, a.Me.Workspace.ID, "Synthetic registry restore custody", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	h := wire.Handshake{ProtocolVersion: wire.Version, Capabilities: []string{wire.CollectFacts}, Facts: wire.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "synthetic-registry-restore", LogicalCPUs: 1, MemoryTotalBytes: 1024, DiskPath: "/", DiskTotalBytes: 1024, DiskAvailableBytes: 512, ObservedAt: time.Now().UTC(), AgentVersion: wire.AgentVersion}}
	node, e := n.Enroll(ctx, wire.Enrollment{JoinToken: token.Token, Handshake: h}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	task, e := n.CreateTask(ctx, a, node.NodeID, wire.CollectFacts, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	tenant, workspace, user := a.Me.Workspace.TenantID, a.Me.Workspace.ID, a.Me.User.ID
	model, deployment, pin, grant := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by) VALUES($1,$2,$3,'Synthetic registry authority metadata','qwen3-0.6b',$5,'{}','ready',$4)`, []any{model, tenant, workspace, user, lifecyclewire.ApprovedModelCatalogSHA256}},
		{`INSERT INTO deployments(id,tenant_id,workspace_id,node_id,model_id,gpu_uuid,desired_state,observed_state,revision,created_by) VALUES($1,$2,$3,$4,$5,$6,'stopped','stopped',1,$7)`, []any{deployment, tenant, workspace, node.NodeID, model, "GPU-" + uuid.NewString(), user}},
		{`INSERT INTO gpu_leases(tenant_id,workspace_id,node_id,gpu_uuid,deployment_id,revision,state) SELECT tenant_id,workspace_id,node_id,gpu_uuid,id,1,'recovery_required' FROM deployments WHERE id=$1`, []any{deployment}},
		{`INSERT INTO image_registry_pins(id,tenant_id,workspace_id,asset_id,source_archive_sha256,source_manifest_sha256,backend_identity,state,created_by,actor_epoch) VALUES($1,$2,$3,'synthetic-metadata-only',repeat('b',64),repeat('c',64),'sha256:'||repeat('d',64),'importing',$4,1)`, []any{pin, tenant, workspace, user}},
		{`INSERT INTO image_registry_deployment_refs(tenant_id,workspace_id,pin_id,node_id,deployment_id,created_revision) VALUES($1,$2,$3,$4,$5,1)`, []any{tenant, workspace, pin, node.NodeID, deployment}},
		{`INSERT INTO image_registry_grants(id,tenant_id,workspace_id,pin_id,node_id,deployment_id,task_id,revision,created_by,actor_epoch,created_session_id,created_token_hash,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,1,$8,1,$9,decode(repeat('e',64),'hex'),clock_timestamp()+interval '1 hour')`, []any{grant, tenant, workspace, pin, node.NodeID, deployment, task.ID, user, uuid.NewString()}},
	} {
		if _, e = s.DB.Exec(ctx, q.sql, q.args...); e != nil {
			t.Fatal("constrained synthetic registry custody", e)
		}
	}
	var before string
	if e = s.DB.QueryRow(ctx, `SELECT (to_jsonb(g)-'revoked_at')::text FROM image_registry_grants g WHERE id=$1`, grant).Scan(&before); e != nil {
		t.Fatal(e)
	}
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if e = recovery.InvalidateRestoredWithin(ctx, s, transaction.PostgreSQLTransaction(tx), "Synthetic full restore late-audit refusal"); e != nil {
		tx.Rollback(ctx)
		t.Fatal(e)
	}
	var revoked bool
	if e = tx.QueryRow(ctx, `SELECT revoked_at IS NOT NULL FROM image_registry_grants WHERE id=$1`, grant).Scan(&revoked); e != nil || !revoked {
		tx.Rollback(ctx)
		t.Fatal("registry was not fenced inside original transaction", e)
	}
	if _, e = tx.Exec(ctx, `INSERT INTO audit_events(id,audience,action,outcome,request_id) VALUES($1,'operator','operator.synthetic_final_restore','invalid',$2)`, uuid.NewString(), uuid.NewString()); e == nil {
		tx.Rollback(ctx)
		t.Fatal("last audit failure not injected")
	}
	if e = tx.Rollback(ctx); e != nil && !isClosedTransaction(e) {
		t.Fatal(e)
	}
	if e = s.DB.QueryRow(ctx, `SELECT revoked_at IS NOT NULL FROM image_registry_grants WHERE id=$1`, grant).Scan(&revoked); e != nil || revoked {
		t.Fatal("registry grant escaped original rollback", e)
	}
	if _, e = s.UpdateMe(ctx, a, a.Me.User.DisplayName, "en", a.Me.User.Version, uuid.NewString()); e != nil {
		t.Fatal("account authority escaped original rollback", e)
	}
	if e = recovery.InvalidateRestored(ctx, s, "Synthetic full restore registry revocation"); e != nil {
		t.Fatal(e)
	}
	var after, state string
	var refs int
	if e = s.DB.QueryRow(ctx, `SELECT revoked_at IS NOT NULL,(to_jsonb(g)-'revoked_at')::text FROM image_registry_grants g WHERE id=$1`, grant).Scan(&revoked, &after); e != nil || !revoked || after != before {
		t.Fatal("grant identity changed or authority survived", e)
	}
	if e = s.DB.QueryRow(ctx, `SELECT state FROM gpu_leases WHERE deployment_id=$1`, deployment).Scan(&state); e != nil || state != "recovery_required" {
		t.Fatal("uncertain physical custody released", e)
	}
	if e = s.DB.QueryRow(ctx, `SELECT count(*) FROM image_registry_deployment_refs WHERE deployment_id=$1 AND released_at IS NULL`, deployment).Scan(&refs); e != nil || refs != 1 {
		t.Fatal("physical registry reference removed", e)
	}
	if _, e = s.UpdateMe(ctx, a, a.Me.User.DisplayName, "en", a.Me.User.Version, uuid.NewString()); e == nil {
		t.Fatal("original session survived committed restore")
	}
}
func isClosedTransaction(e error) bool { return e == pgx.ErrTxClosed }
