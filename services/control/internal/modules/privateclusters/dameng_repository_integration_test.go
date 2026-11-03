//go:build cgo && odbc_manager && infrastructure_dameng_integration

package privateclusters

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/dameng"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// The operator supplies a migrated disposable native schema. This profile never
// provisions a cluster, issues provider requests, executes DDL or claims Job proof.
func privateClusterNativeStore(t *testing.T) transaction.Store {
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

type clusterNativeIDs struct {
	tenant, workspace, user, connection, run, session string
	token                                             []byte
}

func clusterNativeSeed(t *testing.T, ctx context.Context, q transaction.Executor) (clusterNativeIDs, error) {
	t.Helper()
	remote := syntheticClient()
	request := syntheticRun(t, remote)
	p := remote.profile
	raw, e := json.Marshal(request)
	if e != nil {
		return clusterNativeIDs{}, e
	}
	v := clusterNativeIDs{uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), request.ID, uuid.NewString(), []byte(strings.Repeat("t", 32))}
	hash := strings.Repeat("a", 64)
	statements := []transaction.Statement{
		transaction.SQL(transaction.Dameng, `INSERT INTO tenants(id,name,status) VALUES(?,'Synthetic native cluster','active')`, v.tenant),
		transaction.SQL(transaction.Dameng, `INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,'Synthetic native cluster','active')`, v.workspace, v.tenant),
		transaction.SQL(transaction.Dameng, `INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES(?,?,?,'Synthetic native cluster','en','active',1)`, v.user, v.user+"@example.invalid", v.user+"@example.invalid"),
		transaction.SQL(transaction.Dameng, `INSERT INTO private_cluster_connections(id,tenant_id,workspace_id,profile_id,cluster_uid,namespace_uid,namespace_name,installation_id,profile,config_sha256,credential_sha256,manifest_sha256,image_digest,config_digest,state,revision,created_by) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,'active',1,?)`, v.connection, v.tenant, v.workspace, p.ID, p.ClusterUID, p.NamespaceUID, p.Namespace, p.InstallationID, p.Profile, hash, hash, request.ManifestSHA, request.ImageDigest, request.ConfigDigest, v.user),
		transaction.SQL(transaction.Dameng, `INSERT INTO private_cluster_runs(id,tenant_id,workspace_id,connection_id,connection_revision,created_by,actor_epoch,created_session_id,created_token_hash,config_sha256,request,spec_sha256,phase,revision) VALUES(?,?,?,?,1,?,1,?,?,?,CAST(? AS CLOB),?,'prepared',1)`, v.run, v.tenant, v.workspace, v.connection, v.user, v.session, v.token, hash, string(raw), request.SpecSHA),
		transaction.SQL(transaction.Dameng, `UPDATE private_cluster_connections SET active_run_id=?,revision=revision+1 WHERE id=?`, v.run, v.connection),
	}
	for _, s := range statements {
		if _, e = q.Exec(ctx, s); e != nil {
			return v, e
		}
	}
	return v, nil
}

type clusterNativeRejection struct{ cause error }

func (e *clusterNativeRejection) Error() string { return "EXPECTED_NATIVE_INTEGRITY_REJECTION" }
func (e *clusterNativeRejection) Unwrap() error { return e.cause }
func clusterExpectRejection(e error) error {
	if e == nil {
		return nil
	}
	return &clusterNativeRejection{e}
}
func TestNativeDMPrivateClusterLedger(t *testing.T) {
	store := privateClusterNativeStore(t)
	scenarios := []struct {
		name      string
		violation bool
		run       func(context.Context, transaction.Executor, clusterNativeIDs) error
	}{
		{"retained_request_fence_and_credential_recovery", false, func(ctx context.Context, q transaction.Executor, v clusterNativeIDs) error {
			c, e := connectionWithin(ctx, q, v.tenant, v.workspace, v.connection)
			if e != nil {
				return e
			}
			r, e := runWithin(ctx, q, v.tenant, v.workspace, v.connection, v.run)
			if e != nil {
				return e
			}
			if e = validRun(r, c); e != nil {
				return e
			}
			for _, page := range []transaction.Statement{
				connectionPageStatement(q, v.tenant, v.workspace, "cursor", c.CreatedAt, c.ID, 2),
				runPageStatement(q, v.tenant, v.workspace, v.connection, "cursor", r.CreatedAt, r.ID, 2),
			} {
				rows, e := q.Query(ctx, page)
				if e != nil {
					return e
				}
				more := rows.Next()
				e = rows.Err()
				rows.Close()
				if e != nil {
					return e
				}
				if more {
					return errors.New("native keyset repeated boundary row")
				}
			}
			if _, e = runWithin(ctx, q, uuid.NewString(), v.workspace, v.connection, v.run); e == nil || e.Error() != "RESOURCE_NOT_FOUND" {
				return errors.New("cross-tenant run read")
			}
			a := identity.Auth{SessionID: v.session, Epoch: 1, TokenHash: v.token, Me: identity.Me{User: identity.User{ID: v.user}, Audience: "admin"}}
			w := workspaces.Workspace{TenantID: v.tenant, ID: v.workspace}
			m := Mutation{Key: uuid.NewString(), RequestID: uuid.NewString(), Reason: "Synthetic native intent"}
			if e = recordIntent(ctx, q, a, w, "start", v.run, 1, m, []string{"intent"}); e != nil {
				return e
			}
			if intent, replay, e := replayWithin(ctx, q, a, w, "start", m, []string{"intent"}); e != nil || !replay || intent.target != v.run {
				if e != nil {
					return e
				}
				return errors.New("native intent replay changed")
			}
			r, e = saveRun(ctx, q, r, "lease_pending", nil, nil)
			if e != nil {
				return e
			}
			remote := remoteState{Lease: remoteRef{UID: uuid.NewString(), ResourceVersion: "1"}, Logs: strings.Repeat("\u539f\u751f😀", 2048)}
			r, e = saveRun(ctx, q, r, "lease_acquired", nil, &remote)
			if e != nil {
				return e
			}
			if r.Remote == nil || r.Remote.Logs != remote.Logs || r.LeaseUID == nil || *r.LeaseUID != remote.Lease.UID {
				return errors.New("native remote CLOB mismatch")
			}
			before := r
			if e = InvalidateAuthoritiesWithin(ctx, q); e != nil {
				return e
			}
			c, e = connectionWithin(ctx, q, v.tenant, v.workspace, v.connection)
			if e != nil {
				return e
			}
			r, e = runWithin(ctx, q, v.tenant, v.workspace, v.connection, v.run)
			if e != nil {
				return e
			}
			if c.State != "restore_fenced" || c.ActiveRunID == nil || *c.ActiveRunID != v.run || !reflect.DeepEqual(r, before) {
				return errors.New("restore fence changed retained authority/history")
			}
			if e = original(r, a, nil); e != nil {
				return e
			}
			a.SessionID = uuid.NewString()
			if original(r, a, nil) == nil {
				return errors.New("replacement session revived historical authority")
			}
			// Explicit same-scope recovery may rotate credentials without changing intent.
			if _, e = q.Exec(ctx, transaction.SQL(transaction.Dameng, `UPDATE private_cluster_connections SET credential_sha256=?,state='active',revision=revision+1 WHERE id=?`, strings.Repeat("b", 64), v.connection)); e != nil {
				return e
			}
			if r.CreatedAt.Location() != time.UTC || r.CreatedAt.Nanosecond()%1000 != 0 {
				return errors.New("native timestamp boundary")
			}
			return nil
		}},
		{"request_clob_immutable", true, func(ctx context.Context, q transaction.Executor, v clusterNativeIDs) error {
			r, e := runWithin(ctx, q, v.tenant, v.workspace, v.connection, v.run)
			if e != nil {
				return e
			}
			if _, e = saveRun(ctx, q, r, "lease_pending", nil, nil); e != nil {
				return e
			}
			_, e = q.Exec(ctx, transaction.SQL(transaction.Dameng, `UPDATE private_cluster_runs SET request=CAST(? AS CLOB),revision=revision+1 WHERE id=?`, `{}`, v.run))
			return clusterExpectRejection(e)
		}},
		{"illegal_phase_rejected", true, func(ctx context.Context, q transaction.Executor, v clusterNativeIDs) error {
			r, e := runWithin(ctx, q, v.tenant, v.workspace, v.connection, v.run)
			if e != nil {
				return e
			}
			_, e = saveRun(ctx, q, r, "job_absent", nil, &remoteState{Lease: remoteRef{UID: uuid.NewString(), ResourceVersion: "1"}})
			return clusterExpectRejection(e)
		}},
		{"active_pointer_scope_rejected", true, func(ctx context.Context, q transaction.Executor, v clusterNativeIDs) error {
			other := uuid.NewString()
			// The target run exists, but belongs to another connection. An
			// ID-only FK would incorrectly permit this binding.
			_, e := q.Exec(ctx, transaction.SQL(transaction.Dameng, `INSERT INTO private_cluster_connections(id,tenant_id,workspace_id,profile_id,cluster_uid,namespace_uid,namespace_name,installation_id,profile,config_sha256,credential_sha256,manifest_sha256,image_digest,config_digest,state,revision,created_by) SELECT ?,tenant_id,workspace_id,'other-native-test',?,?,namespace_name,installation_id,profile,config_sha256,credential_sha256,manifest_sha256,image_digest,config_digest,'active',1,created_by FROM private_cluster_connections WHERE id=?`, other, uuid.NewString(), uuid.NewString(), v.connection))
			if e != nil {
				return e
			}
			_, e = q.Exec(ctx, transaction.SQL(transaction.Dameng, `UPDATE private_cluster_connections SET active_run_id=?,revision=revision+1 WHERE id=?`, v.run, other))
			return clusterExpectRejection(e)
		}},
		{"intent_delete_rejected", true, func(ctx context.Context, q transaction.Executor, v clusterNativeIDs) error {
			a := identity.Auth{Me: identity.Me{User: identity.User{ID: v.user}, Audience: "admin"}}
			m := Mutation{Key: uuid.NewString(), RequestID: uuid.NewString(), Reason: "Synthetic native intent"}
			if e := recordIntent(ctx, q, a, workspaces.Workspace{TenantID: v.tenant, ID: v.workspace}, "start", v.run, 1, m, struct{}{}); e != nil {
				return e
			}
			_, e := q.Exec(ctx, transaction.SQL(transaction.Dameng, `DELETE FROM private_cluster_intents WHERE tenant_id=? AND workspace_id=? AND actor_id=? AND idempotency_key=?`, v.tenant, v.workspace, v.user, m.Key))
			return clusterExpectRejection(e)
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
				v, e := clusterNativeSeed(t, ctx, q)
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
				var expected *clusterNativeRejection
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
