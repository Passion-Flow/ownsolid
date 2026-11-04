//go:build repository_integration && registry_operator_integration

package imageregistry

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// The fixture owner supplies an already admitted, exactly marked disposable
// native PostgreSQL database. This test never creates/drops a database or role.
// Real tiny OCI CAS/signatures are synthetic, not approved product images.
func TestActualRegistryOperatorAllWorkspaceSnapshotAndOriginalSession(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
	defer cancel()
	raw, e := config.Secret(os.Getenv("PROJECT_ONE_TEST_POSTGRES_REGISTRY_OPERATOR_DSN_FILE"))
	if e != nil {
		t.Fatal("explicit protected admitted synthetic fixture required")
	}
	pc, e := pgxpool.ParseConfig(strings.TrimSpace(string(raw)))
	if e != nil || pc.ConnConfig.Host != "127.0.0.1" || pc.ConnConfig.Port < 1024 || pc.ConnConfig.User != "projectone_operator" || !regexp.MustCompile(`^p1_registry_operator_[a-f0-9]{32}_test$`).MatchString(pc.ConnConfig.Database) {
		t.Fatal("exact registry operator fixture required")
	}
	scope := strings.TrimSuffix(strings.TrimPrefix(pc.ConnConfig.Database, "p1_registry_operator_"), "_test")
	pc.MaxConns = 3
	pc.ConnConfig.RuntimeParams["search_path"] = "public"
	pc.ConnConfig.RuntimeParams["timezone"] = "UTC"
	pc.ConnConfig.RuntimeParams["statement_timeout"] = "10000"
	pc.ConnConfig.RuntimeParams["lock_timeout"] = "1000"
	pool, e := pgxpool.NewWithConfig(ctx, pc)
	if e != nil {
		t.Fatal(e)
	}
	defer pool.Close()
	var marker string
	var tables int
	if pool.QueryRow(ctx, `SELECT pg_catalog.shobj_description((SELECT oid FROM pg_catalog.pg_database WHERE datname=pg_catalog.current_database()),'pg_database')`).Scan(&marker) != nil || marker != "projectone:m24-registry-operator:"+scope {
		t.Fatal("original fixture ownership marker required")
	}
	if pool.QueryRow(ctx, `SELECT count(*) FROM pg_catalog.pg_tables WHERE schemaname='public'`).Scan(&tables) != nil || tables != 0 {
		t.Fatal("fresh fixture required; retain failed setup without reset")
	}
	if e = database.Migrate(ctx, pool); e != nil {
		t.Fatal("actual current forward lineage", e)
	}
	tenant, user := uuid.NewString(), uuid.NewString()
	workspaces := []string{uuid.NewString(), uuid.NewString()}
	ids := []string{uuid.NewString(), uuid.NewString()}
	c := operatorTestConfig(t)
	images := operatorSyntheticCAS(t, &c, ids)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, e := pool.Exec(ctx, sql, args...); e != nil {
			t.Fatal("owned synthetic fixture SQL", e)
		}
	}
	exec(`INSERT INTO public.tenants(id,name,status) VALUES($1,'Synthetic registry operator tenant','active')`, tenant)
	exec(`INSERT INTO public.users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES($1,'registry-operator@example.invalid','registry-operator@example.invalid','Synthetic registry operator','en','active',true)`, user)
	for i, workspace := range workspaces {
		exec(`INSERT INTO public.workspaces(id,tenant_id,name,status) VALUES($1,$2,$3,'active')`, workspace, tenant, "Synthetic registry Workspace "+string(rune('A'+i)))
		image := images[i]
		inventory, _ := json.Marshal(image)
		exec(`INSERT INTO public.image_registry_pins(id,tenant_id,workspace_id,asset_id,source_archive_sha256,source_manifest_sha256,backend_identity,state,manifest_digest,config_digest,archive_sha256,archive_bytes,inventory,inventory_sha256,created_by,actor_epoch) VALUES($1,$2,$3,$4,$5,$6,$7,'ready',$8,$9,$10,$11,$12::jsonb,$13,$14,1)`, ids[i], tenant, workspace, image.Asset.ID, c.ReleaseArchiveSHA256, c.ReleaseManifestSHA256, "sha256:"+strings.Repeat("a", 64), image.Inventory.ManifestDigest, image.Inventory.ConfigDigest, image.Archive.TransportSHA256, image.Archive.TransportBytes, string(inventory), release.Digest(inventory), user)
		exec(`INSERT INTO public.image_registry_imports(tenant_id,workspace_id,pin_id,owner_token,phase) VALUES($1,$2,$3,$4,'completed')`, tenant, workspace, ids[i], uuid.NewString())
		for ordinal, d := range image.Descriptors {
			exec(`INSERT INTO public.image_registry_blobs(tenant_id,workspace_id,pin_id,ordinal,digest,size,media_type,kind) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, tenant, workspace, ids[i], ordinal, d.Digest, d.Size, d.MediaType, d.Kind)
		}
		exec(`INSERT INTO public.image_registry_offline_refs(id,tenant_id,workspace_id,pin_id,created_by) VALUES($1,$2,$3,$4,$5)`, uuid.NewString(), tenant, workspace, ids[i], user)
	}
	// Retained execution reference plus already revoked grant: import must preserve
	// both physical custody and the original immutable revocation timestamp.
	node, model, deployment, task, grant := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	exec(`INSERT INTO public.managed_nodes(id,tenant_id,workspace_id,name,status,protocol_version,facts,last_heartbeat_at,credential_hash) VALUES($1,$2,$3,'Synthetic stopped registry node','active','1.5','{}'::jsonb,clock_timestamp(),$4)`, node, tenant, workspaces[0], make([]byte, 32))
	exec(`INSERT INTO public.model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by) VALUES($1,$2,$3,'Synthetic metadata only','qwen3-0.6b',$4,'{}'::jsonb,'ready',$5)`, model, tenant, workspaces[0], agentwire.ModelCatalogDigest(), user)
	exec(`INSERT INTO public.node_tasks(id,tenant_id,workspace_id,node_id,operation,state,idempotency_key,requested_by,deadline_at,completed_at) VALUES($1,$2,$3,$4,'collect_facts.v1','succeeded',$5,$6,clock_timestamp()+interval '1 hour',clock_timestamp())`, task, tenant, workspaces[0], node, uuid.NewString(), user)
	exec(`INSERT INTO public.deployments(id,tenant_id,workspace_id,node_id,model_id,gpu_uuid,desired_state,observed_state,revision,created_by) VALUES($1,$2,$3,$4,$5,$6,'stopped','stopped',1,$7)`, deployment, tenant, workspaces[0], node, model, "GPU-"+uuid.NewString(), user)
	exec(`INSERT INTO public.image_registry_deployment_refs(tenant_id,workspace_id,pin_id,node_id,deployment_id,created_revision) VALUES($1,$2,$3,$4,$5,1)`, tenant, workspaces[0], ids[0], node, deployment)
	exec(`INSERT INTO public.image_registry_grants(id,tenant_id,workspace_id,pin_id,node_id,deployment_id,task_id,revision,created_by,actor_epoch,created_session_id,created_token_hash,expires_at,revoked_at) VALUES($1,$2,$3,$4,$5,$6,$7,1,$8,1,$9,$10,clock_timestamp()+interval '1 hour',clock_timestamp())`, grant, tenant, workspaces[0], ids[0], node, deployment, task, user, uuid.NewString(), make([]byte, 32))
	var revoked time.Time
	if pool.QueryRow(ctx, `SELECT revoked_at FROM public.image_registry_grants WHERE id=$1`, grant).Scan(&revoked) != nil {
		t.Fatal("original revoked grant required")
	}
	exec(`UPDATE public.installation_state SET bootstrap_complete=true,tenant_id=$1,default_workspace_id=$2,primary_admin_user_id=$3,bootstrapped_at=clock_timestamp() WHERE id=1`, tenant, workspaces[0], user)
	lease, e := pool.Acquire(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer lease.Release()
	var held bool
	if lease.QueryRow(ctx, `SELECT pg_catalog.pg_try_advisory_lock($1)`, database.InstallationLockID).Scan(&held) != nil || !held {
		t.Fatal("original installation lock required")
	}
	defer func() {
		var released bool
		if lease.QueryRow(ctx, `SELECT pg_catalog.pg_advisory_unlock($1)`, database.InstallationLockID).Scan(&released) != nil || !released {
			t.Error("original fixture lock release unconfirmed")
		}
	}()
	b := OperatorSnapshotBinding{ApplicationProfile: operatorApplicationProfile, TenantID: tenant, Database: pc.ConnConfig.Database, Operator: pc.ConnConfig.User, ScopeID: scope}
	if lease.QueryRow(ctx, `SELECT (SELECT system_identifier::text FROM pg_catalog.pg_control_system()),d.oid,pg_catalog.pg_backend_pid() FROM pg_catalog.pg_database d WHERE datname=pg_catalog.current_database()`).Scan(&b.SystemIdentifier, &b.DatabaseOID, &b.BackendPID) != nil {
		t.Fatal("original native source identity required")
	}
	owner, e := NewOperatorSnapshot(c, b)
	if e != nil {
		t.Fatal(e)
	}
	within := func(connection *pgxpool.Conn, mode pgx.TxAccessMode, role string, fn func(transaction.Executor) error) error {
		tx, e := connection.BeginTx(ctx, pgx.TxOptions{AccessMode: mode, IsoLevel: pgx.RepeatableRead})
		if e != nil {
			t.Fatal(e)
		}
		if role != "" {
			if _, e = tx.Exec(ctx, role); e != nil {
				tx.Rollback(ctx)
				t.Fatal(e)
			}
		}
		result := fn(transaction.PostgreSQLTransaction(tx))
		var alive int
		if result == nil && (tx.QueryRow(ctx, `SELECT 1`).Scan(&alive) != nil || alive != 1) {
			t.Fatal("module ended caller transaction")
		}
		if e = tx.Rollback(ctx); e != nil {
			t.Fatal(e)
		}
		return result
	}
	var before []operatorMetadata
	var receipt OperatorSnapshotReceipt
	snapshot := filepath.Join(c.Verification.MountRoot, "snapshot")
	if e = within(lease, pgx.ReadOnly, "", func(q transaction.Executor) error {
		var e error
		before, e = owner.metadata(ctx, q)
		if e != nil {
			return e
		}
		if e = owner.VerifyStoredWithin(ctx, q); e != nil {
			return e
		}
		receipt, e = owner.ExportFilesWithin(ctx, q, snapshot)
		return e
	}); e != nil {
		t.Fatal("real all-Workspace export", e)
	}
	if receipt.Pins != 2 || receipt.Files < 12 || receipt.Bytes < 1 {
		t.Fatal("nonempty Workspace omitted", receipt)
	}
	if _, e = VerifyOperatorSnapshot(ctx, c, snapshot, receipt); e != nil {
		t.Fatal("independent snapshot readback", e)
	}
	targetConfig := c
	targetConfig.StoreRoot = filepath.Join(c.Verification.MountRoot, "fresh-target")
	if e = os.Mkdir(targetConfig.StoreRoot, 0700); e != nil {
		t.Fatal(e)
	}
	targetBinding := b
	targetBinding.ScopeID = strings.ReplaceAll(uuid.NewString(), "-", "")
	target, e := NewOperatorSnapshot(targetConfig, targetBinding)
	if e != nil {
		t.Fatal(e)
	}
	var imported OperatorSnapshotImportReceipt
	if e = within(lease, pgx.ReadOnly, "", func(q transaction.Executor) error {
		var e error
		imported, e = target.ImportFilesWithin(ctx, q, snapshot, receipt)
		return e
	}); e != nil {
		t.Fatal("real original CAS import", e)
	}
	if !imported.Complete || ReadbackOperatorImport(ctx, targetConfig, imported) != nil {
		t.Fatal("independent target completion readback")
	}
	var stillRevoked time.Time
	var retained int
	if lease.QueryRow(ctx, `SELECT revoked_at FROM public.image_registry_grants WHERE id=$1`, grant).Scan(&stillRevoked) != nil || !stillRevoked.Equal(revoked) {
		t.Fatal("import reauthorized or altered revoked grant")
	}
	if lease.QueryRow(ctx, `SELECT count(*) FROM public.image_registry_deployment_refs WHERE released_at IS NULL`).Scan(&retained) != nil || retained != 1 {
		t.Fatal("retained physical execution reference lost")
	}

	if e = within(lease, pgx.ReadOnly, "", func(q transaction.Executor) error {
		after, e := owner.metadata(ctx, q)
		if e != nil {
			return e
		}
		if !reflect.DeepEqual(before, after) {
			t.Fatal("snapshot changed original seven-table metadata")
		}
		_, e = target.ImportFilesWithin(ctx, q, snapshot, receipt)
		if e == nil {
			t.Fatal("completed target silently replayed")
		}
		return target.VerifyStoredWithin(ctx, q)
	}); e != nil {
		t.Fatal(e)
	}
	if e = within(lease, pgx.ReadWrite, "", func(q transaction.Executor) error { return owner.VerifyStoredWithin(ctx, q) }); e == nil {
		t.Fatal("read-write authority admitted")
	}
	other, e := pool.Acquire(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if e = within(other, pgx.ReadOnly, "", func(q transaction.Executor) error { return owner.VerifyStoredWithin(ctx, q) }); e == nil {
		t.Fatal("foreign backend borrowed original lock")
	}
	other.Release()
	if e = within(lease, pgx.ReadOnly, "SET LOCAL ROLE pg_read_all_data", func(q transaction.Executor) error { return owner.VerifyStoredWithin(ctx, q) }); e == nil {
		t.Fatal("nonoperator role admitted")
	}
	// An explicit unfinished original pin remains quarantined, never omitted.
	if _, e = lease.Exec(ctx, `UPDATE public.image_registry_pins SET state='unknown',revision=revision+1 WHERE id=$1`, ids[0]); e != nil {
		t.Fatal(e)
	}
	if _, e = lease.Exec(ctx, `UPDATE public.image_registry_imports SET phase='unknown' WHERE pin_id=$1`, ids[0]); e != nil {
		t.Fatal(e)
	}
	if e = within(lease, pgx.ReadOnly, "", func(q transaction.Executor) error {
		_, e := owner.ExportFilesWithin(ctx, q, filepath.Join(c.Verification.MountRoot, "unknown-snapshot"))
		return e
	}); e == nil || e.Error() != "REGISTRY_RECOVERY_REQUIRED" {
		t.Fatal("uncertain original state omitted/promoted", e)
	}
	if _, e = os.Stat(filepath.Join(c.Verification.MountRoot, "unknown-snapshot")); !os.IsNotExist(e) {
		t.Fatal("failed snapshot published bytes")
	}
	t.Logf("actual native registry module: profile=%s Workspace=2 pins=%d files=%d bytes=%d original_backend=%d; synthetic OCI only; fresh filesystem on same SQL fixture, not native full restore", b.ApplicationProfile, receipt.Pins, receipt.Files, receipt.Bytes, b.BackendPID)
}
