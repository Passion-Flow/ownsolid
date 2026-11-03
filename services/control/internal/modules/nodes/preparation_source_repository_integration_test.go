//go:build repository_integration

package nodes_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	agent "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	lifecycle "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
	training "github.com/Passion-Flow/ownsolid/api/training/v1"
)

// Only the enrollment and SQL/protocol path is real. This valid synthetic
// command does not attest an image, physical task, trained model or quality gate.
func preparationSourceCommand(t *testing.T, x nodeReadActual) lifecycle.Command {
	t.Helper()
	id, digest := uuid.NewString(), strings.Repeat("a", 64)
	image := "sha256:" + strings.Repeat("b", 64)
	cfg, train := lifecycle.DefaultConfiguration(), training.DefaultConfiguration()
	cfgSHA, e := cfg.Digest()
	if e != nil {
		t.Fatal(e)
	}
	trainSHA, e := train.Digest()
	if e != nil {
		t.Fatal(e)
	}
	c := lifecycle.Command{SchemaVersion: 1, Scope: lifecycle.Scope{TenantID: x.f.Tenant, WorkspaceID: x.f.Workspace, PreparationID: id}, AttemptID: uuid.NewString(), AttemptNumber: 1, ModelID: uuid.NewString(), ModelCatalogSHA256: lifecycle.ApprovedModelCatalogSHA256, TrainingJobID: uuid.NewString(), TrainingAttemptID: uuid.NewString(), TrainingConfiguration: train, TrainingConfigurationSHA256: trainSHA, HeldoutID: uuid.NewString(), HeldoutManifestSHA256: digest, GPUUUID: "GPU-" + uuid.NewString(), Configuration: cfg, ConfigurationSHA256: cfgSHA,
		Recipe: lifecycle.ExecutionRecipe{SchemaVersion: 1, WorkerVersion: lifecycle.WorkerVersion, ImageReference: lifecycle.ImageRepository + "@sha256:" + digest, Identity: training.ExecutionIdentity{Profile: training.ProductionProfile, Python: training.PythonVersion, WorkerSourceSHA256: digest, ImageID: &image}, Resources: lifecycle.DefaultResources(), DataLimits: datawire.Limits{MaximumBytes: 1048576, MaximumRows: 256, MaximumLineBytes: 16384, MaximumTextBytes: 4096, MaximumSourceBytes: 256, MaximumErrors: 10}, RecipeSHA256: lifecycle.RecipeDigest(), ProcessorSHA256: lifecycle.ProcessorDigest()},
		Inputs: lifecycle.InputInventory{AdapterFiles: []datawire.Asset{{Path: "README.md", SizeBytes: 248, SHA256: digest}, {Path: "adapter_config.json", SizeBytes: 1092, SHA256: digest}, {Path: "adapter_model.safetensors", SizeBytes: 2308432, SHA256: digest}}, Heldout: datawire.Asset{Path: "heldout.jsonl", SizeBytes: 512, SHA256: digest}}}
	if e := c.Validate(); e != nil {
		t.Fatal(e)
	}
	return c
}

type preparationSourceStory struct {
	x       nodeReadActual
	n       nodes.Node
	task    string
	command lifecycle.Command
}

func preparationSourceSetup(t *testing.T) preparationSourceStory {
	t.Helper()
	x := newNodeReadActual(t)
	token := nodeEnrollmentMint(t, x, x.f.Workspace, "Retained lifecycle source", agent.DockerModelLifecycleProfile)
	enrolled, e := x.s.Enroll(x.ctx, agent.Enrollment{JoinToken: token.Token, Handshake: nodeEnrollmentInput(agent.DockerModelLifecycleProfile)}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	n, e := x.s.Get(x.ctx, x.admin, x.f.Workspace, enrolled.NodeID)
	if e != nil {
		t.Fatal(e)
	}
	c := preparationSourceCommand(t, x)
	raw, e := json.Marshal(c)
	if e != nil {
		t.Fatal(e)
	}
	task, now := uuid.NewString(), time.Now().UTC()
	// Explicit synthetic stopped task history; no generic lease or agent execution.
	_, e = x.f.Store.Exec(x.ctx, x.f.Statement(`INSERT INTO node_tasks(id,tenant_id,workspace_id,node_id,operation,state,idempotency_key,requested_by,deadline_at,payload,request_fingerprint,completed_at) VALUES($1,$2,$3,$4,'model.prepare.v1','succeeded',$5,$6,$7,$8,$9,$10)`, `INSERT INTO node_tasks(id,tenant_id,workspace_id,node_id,operation,state,idempotency_key,requested_by,deadline_at,payload,request_fingerprint,completed_at) VALUES(?,?,?,?,'model.prepare.v1','succeeded',?,?,?,?,?,?)`, task, x.f.Tenant, x.f.Workspace, n.ID, uuid.NewString(), x.f.User, now.Add(time.Minute), raw, make([]byte, 32), now))
	if e != nil {
		t.Fatal(e)
	}
	return preparationSourceStory{x, n, task, c}
}
func (g preparationSourceStory) within(t *testing.T, fn func(context.Context, transaction.Executor) error) error {
	t.Helper()
	return g.x.f.Store.Within(g.x.ctx, false, func(ctx context.Context, q transaction.Executor) error {
		if e := g.x.owner.RecheckOrderedWithin(ctx, q, g.x.admin); e != nil {
			return e
		}
		if _, e := g.x.s.Workspaces.RecheckScopeWithin(ctx, q, g.x.admin, g.x.f.Workspace); e != nil {
			return e
		}
		return fn(ctx, q)
	})
}
func preparationSourceNoTask(t *testing.T, task nodes.Task, c lifecycle.Command, e error, code string) {
	t.Helper()
	nodeReadWant(t, e, code)
	if !reflect.DeepEqual(task, nodes.Task{}) || !reflect.DeepEqual(c, lifecycle.Command{}) {
		t.Fatal("failed borrowed source leaked task or command")
	}
}

type preparationSourceWrongDialect struct{ transaction.Executor }

func (q preparationSourceWrongDialect) Dialect() transaction.Dialect {
	if q.Executor.Dialect() == transaction.MySQL {
		return transaction.PostgreSQL
	}
	return transaction.MySQL
}

func TestActualBorrowedPreparationNodeSource(t *testing.T) {
	t.Run("actual_enrollment_typed_target_history_and_task", func(t *testing.T) {
		g := preparationSourceSetup(t)
		audits := g.x.audits(t)
		e := g.within(t, func(ctx context.Context, q transaction.Executor) error {
			n, e := g.x.s.ModelPreparationTargetWithin(ctx, q, g.n.TenantID, g.n.WorkspaceID, g.n.ID)
			if e != nil {
				return e
			}
			if n.ID != g.n.ID || n.ProtocolVersion != agent.ModelLifecycleVersion {
				t.Fatal("actual lifecycle target changed")
			}
			h, e := g.x.s.PreparationHistoryWithin(ctx, q, n.TenantID, n.WorkspaceID, n.ID)
			if e != nil {
				return e
			}
			if !reflect.DeepEqual(h, n) {
				t.Fatal("target/history inconsistent")
			}
			task, c, e := g.x.s.PreparationStopTaskWithin(ctx, q, n, g.task)
			if e != nil {
				return e
			}
			if task.ID != g.task || task.State != "succeeded" || !lifecycle.SameCommand(c, g.command) {
				t.Fatal("retained typed task/command changed")
			}
			return nil
		})
		if e != nil || g.x.audits(t) != audits {
			t.Fatal("borrowed reads committed extra effects", e)
		}
	})
	t.Run("removed_disabled_history_without_execution", func(t *testing.T) {
		g := preparationSourceSetup(t)
		for _, state := range []string{"disabled", "removed"} {
			n, e := g.x.s.Update(g.x.ctx, g.x.admin, g.n.ID, g.n.Name, state, g.n.Version, "Synthetic retained-source state boundary", uuid.NewString())
			if e != nil {
				t.Fatal(e)
			}
			g.n.Version = n.Version
			if e = g.within(t, func(ctx context.Context, q transaction.Executor) error {
				h, e := g.x.s.PreparationHistoryWithin(ctx, q, g.n.TenantID, g.n.WorkspaceID, g.n.ID)
				if e != nil {
					return e
				}
				if h.Status != state {
					t.Fatal("history lost persisted state")
				}
				target, e := g.x.s.ModelPreparationTargetWithin(ctx, q, h.TenantID, h.WorkspaceID, h.ID)
				nodeReadWant(t, e, "NODE_UNAVAILABLE")
				if !reflect.DeepEqual(target, nodes.Node{}) {
					t.Fatal("disabled target escaped")
				}
				task, c, e := g.x.s.PreparationStopTaskWithin(ctx, q, g.n, g.task)
				if e != nil || task.ID != g.task || !lifecycle.SameCommand(c, g.command) {
					t.Fatal("retained stopped history unavailable", e)
				}
				return nil
			}); e != nil {
				t.Fatal(e)
			}
		}
	})
	t.Run("exact_scope_and_active_installed_tenant", func(t *testing.T) {
		g := preparationSourceSetup(t)
		for _, scope := range [][3]string{{uuid.NewString(), g.n.WorkspaceID, g.n.ID}, {g.n.TenantID, uuid.NewString(), g.n.ID}, {g.n.TenantID, g.n.WorkspaceID, uuid.NewString()}, {g.n.TenantID, g.n.WorkspaceID, "bad"}} {
			if e := g.within(t, func(ctx context.Context, q transaction.Executor) error {
				n, e := g.x.s.PreparationHistoryWithin(ctx, q, scope[0], scope[1], scope[2])
				nodeReadWant(t, e, "RESOURCE_NOT_FOUND")
				if !reflect.DeepEqual(n, nodes.Node{}) {
					t.Fatal("foreign source leaked")
				}
				return nil
			}); e != nil {
				t.Fatal(e)
			}
		}
		rollback := errors.New("EXPLICIT_TEST_INACTIVE_SOURCE_ROLLBACK")
		for _, mutation := range [][2]string{{"UPDATE tenants SET status='disabled' WHERE id=$1", "UPDATE tenants SET status='disabled' WHERE id=?"}, {"UPDATE installation_state SET bootstrap_complete=false,tenant_id=NULL,default_workspace_id=NULL,primary_admin_user_id=NULL,bootstrapped_at=NULL WHERE id=$1", "UPDATE installation_state SET bootstrap_complete=0,tenant_id=NULL,default_workspace_id=NULL,primary_admin_user_id=NULL,bootstrapped_at=NULL WHERE id=?"}} {
			e := g.x.f.Store.Within(g.x.ctx, false, func(ctx context.Context, q transaction.Executor) error {
				var key any = g.x.f.Tenant
				if strings.Contains(mutation[0], "installation_state") {
					key = 1
				}
				if _, e := q.Exec(ctx, g.x.f.Statement(mutation[0], mutation[1], key)); e != nil {
					return e
				}
				n, e := g.x.s.PreparationHistoryWithin(ctx, q, g.n.TenantID, g.n.WorkspaceID, g.n.ID)
				nodeReadWant(t, e, "RESOURCE_NOT_FOUND")
				if !reflect.DeepEqual(n, nodes.Node{}) {
					t.Fatal("inactive installation leaked")
				}
				return rollback
			})
			if e != rollback {
				t.Fatal("inactive source test rollback failed", e)
			}
		}
	})
	t.Run("stale_forged_and_legacy_profiles", func(t *testing.T) {
		g := preparationSourceSetup(t)
		g.x.exec(t, "UPDATE managed_nodes SET protocol_version='1.0' WHERE id=$1", "UPDATE managed_nodes SET protocol_version='1.0' WHERE id=?", g.n.ID)
		if e := g.within(t, func(ctx context.Context, q transaction.Executor) error {
			task, c, e := g.x.s.PreparationStopTaskWithin(ctx, q, g.n, g.task)
			preparationSourceNoTask(t, task, c, e, "AGENT_PROFILE_REJECTED")
			n, e := g.x.s.ModelPreparationTargetWithin(ctx, q, g.n.TenantID, g.n.WorkspaceID, g.n.ID)
			nodeReadWant(t, e, "AGENT_PROFILE_REJECTED")
			if !reflect.DeepEqual(n, nodes.Node{}) {
				t.Fatal("stale negotiated source promoted")
			}
			return nil
		}); e != nil {
			t.Fatal(e)
		}
	})
	t.Run("operation_payload_and_command_scope_refusal", func(t *testing.T) {
		g := preparationSourceSetup(t)
		valid, _ := json.Marshal(g.command)
		foreign := g.command
		foreign.Scope.WorkspaceID = uuid.NewString()
		raw, _ := json.Marshal(foreign)
		var unknown map[string]any
		if e := json.Unmarshal(valid, &unknown); e != nil {
			t.Fatal(e)
		}
		unknown["unknown_source"] = true
		unknownRaw, e := json.Marshal(unknown)
		if e != nil {
			t.Fatal(e)
		}
		cases := []struct {
			operation string
			payload   []byte
			code      string
		}{{agent.CollectFacts, valid, "AGENT_PROFILE_REJECTED"}, {agent.ModelPreparationRunOperation, []byte(`{}`), "MODEL_PREPARATION_COMMAND_INVALID"}, {agent.ModelPreparationRunOperation, unknownRaw, "MODEL_PREPARATION_COMMAND_INVALID"}, {agent.ModelPreparationRunOperation, raw, "MODEL_PREPARATION_COMMAND_INVALID"}}
		for _, v := range cases {
			g.x.exec(t, "UPDATE node_tasks SET operation=$1,payload=$2 WHERE id=$3", "UPDATE node_tasks SET operation=?,payload=? WHERE id=?", v.operation, v.payload, g.task)
			if e := g.within(t, func(ctx context.Context, q transaction.Executor) error {
				task, c, e := g.x.s.PreparationStopTaskWithin(ctx, q, g.n, g.task)
				preparationSourceNoTask(t, task, c, e, v.code)
				return nil
			}); e != nil {
				t.Fatal(e)
			}
		}
	})
	t.Run("actual_node_and_task_locks", func(t *testing.T) {
		g := preparationSourceSetup(t)
		for _, table := range []string{"managed_nodes", "node_tasks"} {
			if e := g.within(t, func(ctx context.Context, q transaction.Executor) error {
				if _, _, e := g.x.s.PreparationStopTaskWithin(ctx, q, g.n, g.task); e != nil {
					return e
				}
				id := g.n.ID
				if table == "node_tasks" {
					id = g.task
				}
				blocked, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
				defer cancel()
				start := time.Now()
				_, e := g.x.f.Store.Exec(blocked, g.x.f.Statement("UPDATE "+table+" SET updated_at=clock_timestamp() WHERE id=$1", "UPDATE "+table+" SET updated_at=UTC_TIMESTAMP(6) WHERE id=?", id))
				if e == nil || time.Since(start) < 150*time.Millisecond {
					t.Fatal("borrowed source did not retain exclusive row fence", table, e)
				}
				return nil
			}); e != nil {
				t.Fatal(e)
			}
		}
	})
	t.Run("owner_rollback_and_reopen", func(t *testing.T) {
		g := preparationSourceSetup(t)
		sentinel := errors.New("EXPLICIT_TEST_OWNER_ROLLBACK")
		e := g.within(t, func(ctx context.Context, q transaction.Executor) error {
			if _, _, e := g.x.s.PreparationStopTaskWithin(ctx, q, g.n, g.task); e != nil {
				return e
			}
			if _, e := q.Exec(ctx, g.x.f.Statement("UPDATE managed_nodes SET name='Rolled back source owner' WHERE id=$1", "UPDATE managed_nodes SET name='Rolled back source owner' WHERE id=?", g.n.ID)); e != nil {
				return e
			}
			return sentinel
		})
		if e != sentinel {
			t.Fatal("owner rollback outcome lost", e)
		}
		service := *g.x.s
		if e = g.within(t, func(ctx context.Context, q transaction.Executor) error {
			n, e := service.PreparationHistoryWithin(ctx, q, g.n.TenantID, g.n.WorkspaceID, g.n.ID)
			if e != nil {
				return e
			}
			if n.Name != g.n.Name {
				t.Fatal("borrowed method committed owner transaction")
			}
			_, _, e = service.PreparationStopTaskWithin(ctx, q, n, g.task)
			return e
		}); e != nil {
			t.Fatal(e)
		}
	})
	t.Run("nil_mixed_context_and_query_failure", func(t *testing.T) {
		g := preparationSourceSetup(t)
		if e := g.within(t, func(ctx context.Context, q transaction.Executor) error {
			var absent *nodes.Service
			for _, service := range []*nodes.Service{nil, absent, &nodes.Service{}} {
				n, e := service.PreparationHistoryWithin(ctx, q, g.n.TenantID, g.n.WorkspaceID, g.n.ID)
				if e == nil || !reflect.DeepEqual(n, nodes.Node{}) {
					t.Fatal("missing owner accepted")
				}
			}
			for _, v := range []struct {
				ctx  context.Context
				q    transaction.Executor
				code string
			}{{nil, q, "STORAGE_UNAVAILABLE"}, {ctx, nil, "STORAGE_UNAVAILABLE"}, {ctx, preparationSourceWrongDialect{q}, "NODE_STORAGE_MISMATCH"}} {
				n, e := g.x.s.PreparationHistoryWithin(v.ctx, v.q, g.n.TenantID, g.n.WorkspaceID, g.n.ID)
				nodeReadWant(t, e, v.code)
				if !reflect.DeepEqual(n, nodes.Node{}) {
					t.Fatal("invalid boundary leaked")
				}
			}
			cancelled, cancel := context.WithCancel(ctx)
			cancel()
			task, c, e := g.x.s.PreparationStopTaskWithin(cancelled, q, g.n, g.task)
			preparationSourceNoTask(t, task, c, e, "STORAGE_CANCELLED")
			return nil
		}); e != nil {
			t.Fatal(e)
		}
		// This isolated schema is disposable. A real missing-column query fails on
		// the actual engine; no mocked row or error is substituted.
		g.x.exec(t, "ALTER TABLE node_tasks RENAME COLUMN payload TO source_test_retained_payload", "RENAME TABLE node_tasks TO source_test_retained_tasks")
		defer g.x.exec(t, "ALTER TABLE node_tasks RENAME COLUMN source_test_retained_payload TO payload", "RENAME TABLE source_test_retained_tasks TO node_tasks")
		e := g.within(t, func(ctx context.Context, q transaction.Executor) error {
			task, c, e := g.x.s.PreparationStopTaskWithin(ctx, q, g.n, g.task)
			preparationSourceNoTask(t, task, c, e, "STORAGE_UNAVAILABLE")
			return e
		})
		nodeReadWant(t, e, "STORAGE_UNAVAILABLE")
	})
}
