//go:build repository_integration

package nodes_test

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"reflect"
	"testing"
	"time"
)

func trainingTargetEnroll(t *testing.T, x nodeReadActual, profile string) nodes.Node {
	t.Helper()
	token := nodeEnrollmentMint(t, x, x.f.Workspace, "Actual training inventory target "+uuid.NewString(), profile)
	out, e := x.s.Enroll(x.ctx, wire.Enrollment{JoinToken: token.Token, Handshake: nodeEnrollmentInput(profile)}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	n, e := x.s.Get(x.ctx, x.admin, x.f.Workspace, out.NodeID)
	if e != nil {
		t.Fatal(e)
	}
	return n
}
func trainingTargetWithin(x nodeReadActual, fn func(context.Context, transaction.Executor) error) error {
	return x.f.Store.Within(x.ctx, false, func(ctx context.Context, q transaction.Executor) error {
		if e := x.owner.RecheckOrderedWithin(ctx, q, x.admin); e != nil {
			return e
		}
		if _, e := x.s.Workspaces.RecheckScopeWithin(ctx, q, x.admin, x.f.Workspace); e != nil {
			return e
		}
		return fn(ctx, q)
	})
}
func trainingTargetRefused(t *testing.T, n nodes.Node, e error, code string) {
	t.Helper()
	nodeReadWant(t, e, code)
	if !reflect.DeepEqual(n, nodes.Node{}) {
		t.Fatal("failed target leaked inventory", n)
	}
}
func TestActualPairedTrainingTarget(t *testing.T) {
	t.Run("active_training_profiles_and_UTC_scope", func(t *testing.T) {
		x := newNodeReadActual(t)
		for _, profile := range []string{wire.DockerAIProfile, wire.DockerModelLifecycleProfile} {
			n := trainingTargetEnroll(t, x, profile)
			audits := x.audits(t)
			e := trainingTargetWithin(x, func(ctx context.Context, q transaction.Executor) error {
				got, e := x.s.TrainingTargetWithin(ctx, q, x.f.Tenant, x.f.Workspace, n.ID)
				if e != nil {
					return e
				}
				if got.ID != n.ID || got.TenantID != x.f.Tenant || got.WorkspaceID != x.f.Workspace || got.ExecutionProfile != profile || got.ProtocolVersion != wire.ProfileVersion(profile) || got.CreatedAt.Location() != time.UTC {
					t.Fatal("actual enrolled target scope/profile/UTC", got)
				}
				return nil
			})
			if e != nil || x.audits(t) != audits {
				t.Fatal("target read side effects", e)
			}
		}
	})
	t.Run("current_status_and_profile_refusal", func(t *testing.T) {
		x := newNodeReadActual(t)
		n := trainingTargetEnroll(t, x, wire.DockerModelLifecycleProfile)
		e := trainingTargetWithin(x, func(ctx context.Context, q transaction.Executor) error {
			got, e := x.s.TrainingTargetWithin(ctx, q, x.f.Tenant, x.f.Workspace, x.ids[0])
			trainingTargetRefused(t, got, e, "AGENT_PROFILE_REJECTED")
			return nil
		})
		if e != nil {
			t.Fatal(e)
		}
		x.exec(t, "UPDATE managed_nodes SET protocol_version='1.0' WHERE id=$1", "UPDATE managed_nodes SET protocol_version='1.0' WHERE id=?", n.ID)
		e = trainingTargetWithin(x, func(ctx context.Context, q transaction.Executor) error {
			got, e := x.s.TrainingTargetWithin(ctx, q, x.f.Tenant, x.f.Workspace, n.ID)
			trainingTargetRefused(t, got, e, "AGENT_PROFILE_REJECTED")
			return nil
		})
		if e != nil {
			t.Fatal(e)
		}
		x.exec(t, "UPDATE managed_nodes SET protocol_version=$1 WHERE id=$2", "UPDATE managed_nodes SET protocol_version=? WHERE id=?", wire.ModelLifecycleVersion, n.ID)
		raw, _ := json.Marshal(wire.Capabilities(wire.ReadOnlyProfile))
		if x.f.Store.Dialect() == transaction.PostgreSQL {
			x.exec(t, "UPDATE managed_nodes SET capabilities=$2 WHERE id=$1", "", n.ID, raw)
		} else {
			x.exec(t, "", "UPDATE managed_nodes SET capabilities=CAST(? AS JSON) WHERE id=?", raw, n.ID)
		}
		e = trainingTargetWithin(x, func(ctx context.Context, q transaction.Executor) error {
			got, e := x.s.TrainingTargetWithin(ctx, q, x.f.Tenant, x.f.Workspace, n.ID)
			trainingTargetRefused(t, got, e, "AGENT_PROFILE_REJECTED")
			return nil
		})
		if e != nil {
			t.Fatal(e)
		}
		raw, _ = json.Marshal(wire.Capabilities(wire.DockerModelLifecycleProfile))
		if x.f.Store.Dialect() == transaction.PostgreSQL {
			x.exec(t, "UPDATE managed_nodes SET capabilities=$2 WHERE id=$1", "", n.ID, raw)
		} else {
			x.exec(t, "", "UPDATE managed_nodes SET capabilities=CAST(? AS JSON) WHERE id=?", raw, n.ID)
		}
		for _, state := range []string{"disabled", "removed"} {
			changed, e := x.s.Update(x.ctx, x.admin, n.ID, n.Name, state, n.Version, "Actual current training target refusal", uuid.NewString())
			if e != nil {
				t.Fatal(e)
			}
			n.Version = changed.Version
			e = trainingTargetWithin(x, func(ctx context.Context, q transaction.Executor) error {
				got, e := x.s.TrainingTargetWithin(ctx, q, x.f.Tenant, x.f.Workspace, n.ID)
				trainingTargetRefused(t, got, e, "NODE_UNAVAILABLE")
				return nil
			})
			if e != nil {
				t.Fatal(e)
			}
		}
	})
	t.Run("exact_tenant_workspace_and_installed_scope", func(t *testing.T) {
		x := newNodeReadActual(t)
		n := trainingTargetEnroll(t, x, wire.DockerAIProfile)
		for _, scope := range [][3]string{{uuid.NewString(), x.f.Workspace, n.ID}, {x.f.Tenant, uuid.NewString(), n.ID}, {x.f.Tenant, x.f.Workspace, uuid.NewString()}, {x.f.Tenant, x.f.Workspace, "invalid"}} {
			e := trainingTargetWithin(x, func(ctx context.Context, q transaction.Executor) error {
				got, e := x.s.TrainingTargetWithin(ctx, q, scope[0], scope[1], scope[2])
				trainingTargetRefused(t, got, e, "RESOURCE_NOT_FOUND")
				return nil
			})
			if e != nil {
				t.Fatal(e)
			}
		}
		rollback := errors.New("EXPLICIT_INSTALLED_SCOPE_ROLLBACK")
		e := trainingTargetWithin(x, func(ctx context.Context, q transaction.Executor) error {
			if _, e := q.Exec(ctx, x.f.Statement("UPDATE installation_state SET bootstrap_complete=false,tenant_id=NULL,default_workspace_id=NULL,primary_admin_user_id=NULL,bootstrapped_at=NULL WHERE id=1", "UPDATE installation_state SET bootstrap_complete=0,tenant_id=NULL,default_workspace_id=NULL,primary_admin_user_id=NULL,bootstrapped_at=NULL WHERE id=1")); e != nil {
				return e
			}
			got, e := x.s.TrainingTargetWithin(ctx, q, x.f.Tenant, x.f.Workspace, n.ID)
			trainingTargetRefused(t, got, e, "RESOURCE_NOT_FOUND")
			return rollback
		})
		if e != rollback {
			t.Fatal("installation mutation not rolled back", e)
		}
	})
	t.Run("caller_rollback_cancellation_and_store_coherence", func(t *testing.T) {
		x := newNodeReadActual(t)
		n := trainingTargetEnroll(t, x, wire.DockerAIProfile)
		rollback := errors.New("EXPLICIT_TRAINING_TARGET_OWNER_ROLLBACK")
		e := trainingTargetWithin(x, func(ctx context.Context, q transaction.Executor) error {
			if _, e := q.Exec(ctx, x.f.Statement("UPDATE managed_nodes SET status='disabled' WHERE id=$1", "UPDATE managed_nodes SET status='disabled' WHERE id=?", n.ID)); e != nil {
				return e
			}
			got, e := x.s.TrainingTargetWithin(ctx, q, x.f.Tenant, x.f.Workspace, n.ID)
			trainingTargetRefused(t, got, e, "NODE_UNAVAILABLE")
			return rollback
		})
		if e != rollback {
			t.Fatal(e)
		}
		e = trainingTargetWithin(x, func(ctx context.Context, q transaction.Executor) error {
			got, e := x.s.TrainingTargetWithin(ctx, q, x.f.Tenant, x.f.Workspace, n.ID)
			if e != nil || got.Status != "active" {
				t.Fatal("hidden borrower commit", got, e)
			}
			got, e = x.s.TrainingTargetWithin(ctx, preparationSourceWrongDialect{q}, x.f.Tenant, x.f.Workspace, n.ID)
			trainingTargetRefused(t, got, e, "NODE_STORAGE_MISMATCH")
			return nil
		})
		if e != nil {
			t.Fatal(e)
		}
		ctx, cancel := context.WithCancel(x.ctx)
		cancel()
		got, e := x.s.TrainingTargetWithin(ctx, x.f.Store, x.f.Tenant, x.f.Workspace, n.ID)
		trainingTargetRefused(t, got, e, string(transaction.Cancelled))
		ctx, cancel = context.WithDeadline(x.ctx, time.Now().Add(-time.Second))
		defer cancel()
		got, e = x.s.TrainingTargetWithin(ctx, x.f.Store, x.f.Tenant, x.f.Workspace, n.ID)
		trainingTargetRefused(t, got, e, string(transaction.Timeout))
	})
}
