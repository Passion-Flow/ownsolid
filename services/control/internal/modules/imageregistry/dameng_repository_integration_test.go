//go:build cgo && odbc_manager && infrastructure_dameng_integration

package imageregistry

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/deployments"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/dameng"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// This profile uses a real, operator-migrated disposable native DM schema. No
// compatibility server, DDL execution, network registry or GPU claim is made.
func registryNativeStore(t *testing.T) transaction.Store {
	t.Helper()
	c, e := dameng.LoadConnectionConfiguration(os.Getenv("PROJECT_ONE_DAMENG_CONNECTOR_CONFIGURATION_FILE"), "")
	if e != nil || !strings.HasPrefix(c.Schema, "P1_INFRA_TEST_") || c.User != c.Schema || c.Policy.MaximumParameterBytes < 65536 || c.Policy.MaximumColumns < 22 || c.Policy.MaximumCellBytes < 65536 || c.Policy.MaximumResultBytes < 131072 {
		t.Fatal("explicit disposable native DM owner/schema and bounded CLOB policy required")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Duration(c.OperationSeconds)*time.Second)
	defer cancel()
	kernel, _, e := dameng.Open(ctx, c)
	if kernel != nil {
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Duration(c.CleanupSeconds)*time.Second)
			defer cancel()
			status, e := kernel.Close(ctx)
			if e != nil || status.State != "closed" || status.PhysicalOwners != 0 || status.CleanupJobs != 0 {
				t.Error("native DM cleanup remains pending; retain operator schema")
			}
		})
	}
	if e != nil {
		t.Fatal("native DM admission failed", dameng.FailureCode(e))
	}
	store, e := transaction.FromDameng(kernel)
	if e != nil {
		t.Fatal("native DM repository unavailable")
	}
	return store
}

type registryNativeIDs struct {
	tenant, workspace, user, pin, node, task, model, deployment, session string
	token                                                                []byte
}

func registryNativeSeed(ctx context.Context, q transaction.Executor) (registryNativeIDs, error) {
	v := registryNativeIDs{uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), []byte(strings.Repeat("t", 32))}
	hash := strings.Repeat("a", 64)
	digest := "sha256:" + hash
	statements := []transaction.Statement{
		transaction.SQL(transaction.Dameng, `INSERT INTO tenants(id,name,status) VALUES(?,'Synthetic native registry','active')`, v.tenant),
		transaction.SQL(transaction.Dameng, `INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,'Synthetic native registry','active')`, v.workspace, v.tenant),
		transaction.SQL(transaction.Dameng, `INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES(?,?,?,'Synthetic native registry','en','active',1)`, v.user, v.user+"@example.invalid", v.user+"@example.invalid"),
		transaction.SQL(transaction.Dameng, `INSERT INTO image_registry_pins(id,tenant_id,workspace_id,asset_id,source_archive_sha256,source_manifest_sha256,backend_identity,state,created_by,actor_epoch) VALUES(?,?,?,'native-test',?,?,?,'importing',?,1)`, v.pin, v.tenant, v.workspace, hash, hash, digest, v.user),
		transaction.SQL(transaction.Dameng, `INSERT INTO image_registry_imports(tenant_id,workspace_id,pin_id,owner_token,phase) VALUES(?,?,?,?,'prepared')`, v.tenant, v.workspace, v.pin, uuid.NewString()),
		transaction.SQL(transaction.Dameng, `INSERT INTO managed_nodes(id,tenant_id,workspace_id,name,status,credential_hash,protocol_version,facts,last_heartbeat_at) VALUES(?,?,?,'Synthetic native registry','active',?,'v1',CAST(? AS CLOB),CURRENT_TIMESTAMP(6))`, v.node, v.tenant, v.workspace, v.token, `{}`),
		transaction.SQL(transaction.Dameng, `INSERT INTO node_tasks(id,tenant_id,workspace_id,node_id,operation,state,idempotency_key,requested_by,deadline_at) VALUES(?,?,?,?,'inference.deploy.v1','pending',?,?,?)`, v.task, v.tenant, v.workspace, v.node, uuid.NewString(), v.user, time.Now().UTC().Truncate(time.Microsecond).Add(time.Hour)),
		transaction.SQL(transaction.Dameng, `INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by) VALUES(?,?,?,'Synthetic native registry','qwen3-0.6b',?,CAST(? AS CLOB),'ready',?)`, v.model, v.tenant, v.workspace, hash, `{}`, v.user),
		transaction.SQL(transaction.Dameng, `INSERT INTO deployments(id,tenant_id,workspace_id,node_id,model_id,gpu_uuid,desired_state,revision,last_task_id,created_by,catalog_digest) VALUES(?,?,?,?,?,?,'running',1,?,?,?)`, v.deployment, v.tenant, v.workspace, v.node, v.model, "GPU-"+uuid.NewString(), v.task, v.user, hash),
		transaction.SQL(transaction.Dameng, `INSERT INTO image_registry_deployment_refs(tenant_id,workspace_id,pin_id,node_id,deployment_id,created_revision) VALUES(?,?,?,?,?,1)`, v.tenant, v.workspace, v.pin, v.node, v.deployment),
	}
	for _, s := range statements {
		if _, e := q.Exec(ctx, s); e != nil {
			return v, e
		}
	}
	return v, nil
}
func registryNativeGrant(ctx context.Context, q transaction.Executor, v registryNativeIDs, revoked bool) (string, error) {
	id := uuid.NewString()
	var retired any
	if revoked {
		retired = time.Now().UTC().Truncate(time.Microsecond)
	}
	_, e := q.Exec(ctx, transaction.SQL(transaction.Dameng, `INSERT INTO image_registry_grants(id,tenant_id,workspace_id,pin_id,node_id,deployment_id,task_id,revision,created_by,actor_epoch,created_session_id,created_token_hash,expires_at,revoked_at) VALUES(?,?,?,?,?,?,?,1,?,1,?,?,?,?)`, id, v.tenant, v.workspace, v.pin, v.node, v.deployment, v.task, v.user, v.session, v.token, time.Now().UTC().Truncate(time.Microsecond).Add(time.Hour), retired))
	return id, e
}

type registryNativeRejection struct{ cause error }

func (e *registryNativeRejection) Error() string { return "EXPECTED_NATIVE_INTEGRITY_REJECTION" }
func (e *registryNativeRejection) Unwrap() error { return e.cause }
func registryExpectRejection(e error) error {
	if e == nil {
		return nil
	}
	return &registryNativeRejection{e}
}
func TestNativeDMRegistryLedger(t *testing.T) {
	store := registryNativeStore(t)
	// Each scenario has its own transaction and actual physical session. A native
	// failure is returned immediately; no query follows it in that transaction.
	scenarios := []struct {
		name      string
		violation bool
		run       func(context.Context, transaction.Executor, registryNativeIDs) error
	}{
		{"clob_replay_scoped_lock_and_grant_history", false, func(ctx context.Context, q transaction.Executor, v registryNativeIDs) error {
			hash := strings.Repeat("b", 64)
			raw := `{"label":"` + strings.Repeat("\u539f\u751f😀", 2048) + `"}`
			if _, e := q.Exec(ctx, transaction.SQL(transaction.Dameng, `UPDATE image_registry_pins SET inventory=CAST(? AS CLOB),inventory_sha256=?,manifest_digest=?,config_digest=?,archive_sha256=?,archive_bytes=1,state='ready',revision=revision+1,updated_at=CURRENT_TIMESTAMP(6) WHERE id=?`, raw, hash, "sha256:"+hash, "sha256:"+hash, hash, v.pin)); e != nil {
				return e
			}
			p, e := pinWithin(ctx, q, v.tenant, v.workspace, v.pin)
			if e != nil {
				return e
			}
			if string(p.inventory) != raw || p.State != "ready" || p.Revision != 2 || p.CreatedAt.Location() != time.UTC || p.CreatedAt.Nanosecond()%1000 != 0 {
				return errors.New("native CLOB/time projection mismatch")
			}
			if _, e = pinWithin(ctx, q, uuid.NewString(), v.workspace, v.pin); e == nil || e.Error() != "RESOURCE_NOT_FOUND" {
				return errors.New("cross-tenant pin read")
			}
			a := identity.Auth{SessionID: v.session, Epoch: 1, TokenHash: v.token, Me: identity.Me{User: identity.User{ID: v.user}}}
			w := workspaces.Workspace{TenantID: v.tenant, ID: v.workspace}
			m := Mutation{Key: uuid.NewString()}
			if _, replay, e := mutationWithin(ctx, q, a, w, "grant", v.pin, m, []string{"intent"}); e != nil || replay {
				if e != nil {
					return e
				}
				return errors.New("first mutation replayed")
			}
			if id, replay, e := mutationWithin(ctx, q, a, w, "grant", v.pin, m, []string{"intent"}); e != nil || !replay || id != v.pin {
				if e != nil {
					return e
				}
				return errors.New("mutation replay changed")
			}
			for i := 0; i < 3; i++ {
				if _, e = registryNativeGrant(ctx, q, v, true); e != nil {
					return e
				}
			}
			if _, e = registryNativeGrant(ctx, q, v, false); e != nil {
				return e
			}
			var pin string
			var live bool
			d := deployments.RegistryDeployment{TenantID: v.tenant, WorkspaceID: v.workspace, NodeID: v.node, TaskID: v.task, Revision: 1}
			if e = q.QueryRow(ctx, grantBindingStatement(q, d, a)).Scan(&pin, &live); e != nil {
				return e
			}
			if !live || pin != v.pin {
				return errors.New("native grant authority projection")
			}
			if e = eligibleWithin(ctx, q, p); e == nil || e.Error() != "REGISTRY_REFERENCE_HELD" {
				return errors.New("held deployment reference was removable")
			}
			return nil
		}},
		{"second_live_grant_rejected", true, func(ctx context.Context, q transaction.Executor, v registryNativeIDs) error {
			if _, e := registryNativeGrant(ctx, q, v, false); e != nil {
				return e
			}
			_, e := registryNativeGrant(ctx, q, v, false)
			return registryExpectRejection(e)
		}},
		{"foreign_scope_reference_rejected", true, func(ctx context.Context, q transaction.Executor, v registryNativeIDs) error {
			_, e := q.Exec(ctx, transaction.SQL(transaction.Dameng, `INSERT INTO image_registry_offline_refs(id,tenant_id,workspace_id,pin_id,created_by) VALUES(?,?,?,?,?)`, uuid.NewString(), uuid.NewString(), v.workspace, v.pin, v.user))
			return registryExpectRejection(e)
		}},
		{"pin_provenance_immutable", true, func(ctx context.Context, q transaction.Executor, v registryNativeIDs) error {
			p, e := pinWithin(ctx, q, v.tenant, v.workspace, v.pin)
			if e != nil {
				return e
			}
			if e = stateWithin(ctx, q, p, "unknown"); e != nil {
				return e
			}
			_, e = q.Exec(ctx, transaction.SQL(transaction.Dameng, `UPDATE image_registry_pins SET source_archive_sha256=?,revision=revision+1 WHERE id=?`, strings.Repeat("c", 64), v.pin))
			return registryExpectRejection(e)
		}},
		{"inventory_clob_immutable", true, func(ctx context.Context, q transaction.Executor, v registryNativeIDs) error {
			hash := strings.Repeat("b", 64)
			_, e := q.Exec(ctx, transaction.SQL(transaction.Dameng, `UPDATE image_registry_pins SET inventory=CAST(? AS CLOB),inventory_sha256=?,manifest_digest=?,config_digest=?,archive_sha256=?,archive_bytes=1,state='ready',revision=revision+1 WHERE id=?`, `{"original":true}`, hash, "sha256:"+hash, "sha256:"+hash, hash, v.pin))
			if e != nil {
				return e
			}
			p, e := pinWithin(ctx, q, v.tenant, v.workspace, v.pin)
			if e != nil {
				return e
			}
			if e = stateWithin(ctx, q, p, "unknown"); e != nil {
				return e
			} // Same retained CLOB is legal.
			_, e = q.Exec(ctx, transaction.SQL(transaction.Dameng, `UPDATE image_registry_pins SET inventory=CAST(? AS CLOB),revision=revision+1 WHERE id=?`, `{"replaced":true}`, v.pin))
			return registryExpectRejection(e)
		}},
		{"revoked_grant_never_revives", true, func(ctx context.Context, q transaction.Executor, v registryNativeIDs) error {
			id, e := registryNativeGrant(ctx, q, v, true)
			if e != nil {
				return e
			}
			_, e = q.Exec(ctx, transaction.SQL(transaction.Dameng, `UPDATE image_registry_grants SET revoked_at=NULL WHERE id=?`, id))
			return registryExpectRejection(e)
		}},
	}
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			rollback := errors.New("intentional native fixture rollback")
			seeded := false
			completed := false
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			e := store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
				v, e := registryNativeSeed(ctx, q)
				if e != nil {
					return e
				}
				seeded = true
				e = scenario.run(ctx, q, v)
				if e != nil {
					return e
				}
				completed = true
				return rollback
			})
			if !seeded {
				t.Fatal("native prerequisite seed failed; no qualification", safeError(e))
			}
			if scenario.violation {
				var expected *registryNativeRejection
				var native *transaction.Error
				if completed || !errors.As(e, &expected) || e != expected || !errors.As(e, &native) || !(strings.HasPrefix(native.SQLState, "23") || native.NativeCode == -20001) {
					t.Fatal("expected native integrity violation was not established", safeError(e))
				}
			} else if !completed || e != rollback {
				t.Fatal("native ledger scenario failed", safeError(e))
			}
		})
	}
}
