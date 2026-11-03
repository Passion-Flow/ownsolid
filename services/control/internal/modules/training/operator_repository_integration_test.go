//go:build repository_integration

package training_test

import (
	"bytes"
	"context"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	agent "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/deployments"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

type trainingOperatorStory struct {
	trainingCreatorStory
	job training.Job
}

func trainingOperatorSetup(t *testing.T) trainingOperatorStory {
	t.Helper()
	g := trainingCreatorSetup(t)
	j, e := g.create(uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	g.s.Inference = &deployments.Service{Nodes: g.s.Nodes, Models: g.s.Models}
	return trainingOperatorStory{g, j}
}
func (g trainingOperatorStory) cancel(revision int64) (training.Job, error) {
	return g.s.Cancel(g.ctx, g.a, g.f.Workspace, g.job.ID, training.ActionRequest{ExpectedRevision: revision, Reason: "Actual isolated operator cancellation"}, uuid.NewString())
}
func (g trainingOperatorStory) audits(t *testing.T, want int) {
	t.Helper()
	var n int
	if e := g.f.Store.QueryRow(g.ctx, g.statement("SELECT COUNT(*) FROM audit_events WHERE tenant_id=$1 AND workspace_id=$2 AND action='training.cancel_requested' AND target_type='training_job'", "SELECT COUNT(*) FROM audit_events WHERE tenant_id=? AND workspace_id=? AND action='training.cancel_requested' AND target_type='training_job'", g.f.Tenant, g.f.Workspace)).Scan(&n); e != nil || n != want {
		t.Fatal("actual atomic cancellation audit", n, want, e)
	}
}
func trainingOperatorWrapped(g trainingOperatorStory, store transaction.Store) trainingOperatorStory {
	g.trainingCreatorStory = trainingCreatorWrapped(g.trainingCreatorStory, store)
	if g.s.Inference != nil {
		d := *g.s.Inference
		d.Nodes = g.s.Nodes
		d.Models = g.s.Models
		g.s.Inference = &d
	}
	return g
}
func (g trainingOperatorStory) attempt(t *testing.T, number int) string {
	t.Helper()
	id := uuid.NewString()
	now := time.Now().UTC().Truncate(time.Microsecond)
	g.seed(t, "training_attempts", map[string]any{"id": id, "job_id": g.job.ID, "node_id": g.input.NodeID, "attempt_number": number, "state": "stopped", "requested_by": g.f.User, "requested_audience": "admin", "requested_epoch": 1, "input_inventory": acceptedAdapterJSON(`{}`), "created_at": now, "finished_at": now})
	return id
}
func (g trainingOperatorStory) histories(t *testing.T) []string {
	t.Helper()
	ids := []string{}
	for i := 1; i <= 3; i++ {
		attempt := g.attempt(t, i)
		ids = append(ids, attempt)
		checkpoint := uuid.NewString()
		g.seed(t, "training_uploads", map[string]any{"id": checkpoint, "job_id": g.job.ID, "attempt_id": attempt, "kind": "checkpoint", "global_step": i, "manifest_sha256": strings.Repeat("d", 64), "manifest": acceptedAdapterJSON(`{}`), "manifest_bytes": []byte(`{}`), "state": "accepted"})
		g.seed(t, "training_checkpoints", map[string]any{"id": checkpoint, "job_id": g.job.ID, "attempt_id": attempt, "global_step": i, "receipt_sha256": strings.Repeat("d", 64), "receipt": acceptedAdapterJSON(`{}`), "file_inventory": acceptedAdapterJSON(`{}`), "storage_key": "checkpoints/" + g.job.ID + "/" + attempt + "/" + checkpoint})
		g.seed(t, "training_recoveries", map[string]any{"id": uuid.NewString(), "job_id": g.job.ID, "previous_attempt_id": attempt, "previous_node_id": g.input.NodeID, "next_node_id": g.input.NodeID, "mode": "restart", "requested_by": g.f.User, "reason": "Explicit synthetic retained recovery history", "creation_key": uuid.NewString(), "creation_fingerprint": bytes.Repeat([]byte{7}, 32)})
	}
	return ids
}
func (g trainingOperatorStory) metric(t *testing.T, attempt string, sequence int, raw string) {
	t.Helper()
	g.seed(t, "training_events", map[string]any{"job_id": g.job.ID, "attempt_id": attempt, "sequence": sequence, "kind": "metric", "global_step": sequence, "payload": acceptedAdapterJSON(raw), "canonical_sha256": acceptedAdapterDigest([]byte(raw))})
}
func (g trainingOperatorStory) portal(t *testing.T, role string) identity.Auth {
	t.Helper()
	id := uuid.NewString()
	email := id + "@example.invalid"
	g.exec(t, "INSERT INTO users(id,email_display,email_canonical,display_name,locale,status) VALUES($1,$2,$2,'Synthetic operator reader','en','active')", "INSERT INTO users(id,email_display,email_canonical,display_name,locale,status) VALUES(?,?,?,'Synthetic operator reader','en','active')", operatorUserArguments(g, id, email)...)
	g.exec(t, "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES($1,$2,$3,$4)", "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES(?,?,?,?)", id, g.f.Tenant, g.f.Workspace, role)
	var issued identity.IssuedSession
	e := g.f.Store.Within(g.ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var e error
		issued, e = g.owner.IssueSessionWithin(ctx, q, id, 1, "portal", "", uuid.NewString(), nil)
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	a, e := g.owner.Authenticate(g.ctx, issued.Token, "portal")
	if e != nil {
		t.Fatal(e)
	}
	return a
}
func operatorUserArguments(g trainingOperatorStory, id, email string) []any {
	if g.f.Store.Dialect() == transaction.MySQL {
		return []any{id, email, email}
	}
	return []any{id, email}
}
func (g trainingOperatorStory) openStore(t *testing.T) {
	t.Helper()
	root := g.owner.Config.Training.ArtifactRoot
	if e := os.Mkdir(root, 0700); e != nil {
		t.Fatal(e)
	}
	if e := training.InitializeStore(root, g.f.Tenant, os.Geteuid(), os.Getegid()); e != nil {
		t.Fatal(e)
	}
	resources := wire.DefaultResources()
	store, e := training.OpenStore(root, g.f.Tenant, 4096, resources.MaximumOutputBytes, resources.MaximumOutputFiles)
	if e != nil {
		t.Fatal(e)
	}
	g.s.Store = store
	t.Cleanup(func() {
		if e := store.Close(); e != nil {
			t.Error(e)
		}
	})
}
func (g trainingOperatorStory) hardware(t *testing.T) {
	t.Helper()
	now := time.Now().UTC()
	o := agent.InferenceObservation{GPUs: []agent.NVIDIAGPU{{UUID: g.input.GPUUUID, Name: "Explicit synthetic accelerator observation", DriverVersion: "580.1.0", MemoryTotalMiB: 16384, MemoryFreeMiB: 16384, ComputeCapability: 8, MIGMode: "Disabled", ObservedAt: now}}, ObservedAt: now}
	if e := o.Validate(); e != nil {
		t.Fatal(e)
	}
	g.seed(t, "inference_nodes", map[string]any{"node_id": g.input.NodeID, "observation": acceptedAdapterJSON(acceptedAdapterRaw(t, o))})
}

func TestActualPairedTrainingOperator(t *testing.T) {
	t.Run("actual_queued_cancellation_and_terminal_replay", func(t *testing.T) {
		g := trainingOperatorSetup(t)
		v, e := g.cancel(g.job.Revision)
		if e != nil || v.State != "cancelled" || v.FinishedAt == nil || v.CancelRequestedAt == nil || v.CurrentAttemptID != nil || v.Revision != g.job.Revision+1 {
			t.Fatal("actual queued reduction", v, e)
		}
		g.audits(t, 1)
		same, e := g.cancel(v.Revision)
		if e != nil || !reflect.DeepEqual(v, same) {
			t.Fatal("exact terminal reduction replay", e)
		}
		g.audits(t, 1)
		g.counts(t, 1)
	})
	t.Run("current_roles_epoch_revision_and_scope", func(t *testing.T) {
		g := trainingOperatorSetup(t)
		a := g.portal(t, "viewer")
		v, e := g.s.Cancel(g.ctx, a, g.f.Workspace, g.job.ID, training.ActionRequest{ExpectedRevision: g.job.Revision, Reason: "Viewer may read, not cancel another actor"}, uuid.NewString())
		trainingCreatorWant(t, v, e, "ACCESS_DENIED")
		v, e = g.cancel(g.job.Revision + 1)
		trainingCreatorWant(t, v, e, "TRAINING_REVISION_CONFLICT")
		v, e = g.s.Cancel(g.ctx, g.a, uuid.NewString(), g.job.ID, training.ActionRequest{ExpectedRevision: g.job.Revision, Reason: "Foreign Workspace"}, uuid.NewString())
		trainingCreatorWant(t, v, e, "RESOURCE_NOT_FOUND")
		expired := g.a
		expired.Epoch++
		v, e = g.s.Cancel(g.ctx, expired, g.f.Workspace, g.job.ID, training.ActionRequest{ExpectedRevision: g.job.Revision, Reason: "Stale session"}, uuid.NewString())
		trainingCreatorWant(t, v, e, "AUTH_SESSION_EXPIRED")
		g.exec(t, "UPDATE workspace_memberships SET role='owner' WHERE user_id=$1 AND workspace_id=$2", "UPDATE workspace_memberships SET role='owner' WHERE user_id=? AND workspace_id=?", a.Me.User.ID, g.f.Workspace)
		v, e = g.s.Cancel(g.ctx, a, g.f.Workspace, g.job.ID, training.ActionRequest{ExpectedRevision: g.job.Revision, Reason: "Current owner reduction"}, uuid.NewString())
		if e != nil || v.State != "cancelled" {
			t.Fatal(e)
		}
		g.audits(t, 1)
	})
	t.Run("active_terminal_recovery_and_physical_ownership", func(t *testing.T) {
		g := trainingOperatorSetup(t)
		attempt := g.attempt(t, 1)
		g.exec(t, "UPDATE training_jobs SET state='running',attempt_number=1,current_attempt_id=$1 WHERE id=$2", "UPDATE training_jobs SET state='running',attempt_number=1,current_attempt_id=? WHERE id=?", attempt, g.job.ID)
		g.seed(t, "gpu_leases", map[string]any{"id": uuid.NewString(), "node_id": g.input.NodeID, "gpu_uuid": g.input.GPUUUID, "training_job_id": g.job.ID, "revision": 1, "state": "held"})
		v, e := g.cancel(g.job.Revision)
		if e != nil || v.State != "cancelling" || v.FinishedAt != nil {
			t.Fatal("active physical owner reduction", e)
		}
		var held int
		if e = g.f.Store.QueryRow(g.ctx, g.statement("SELECT COUNT(*) FROM gpu_leases WHERE training_job_id=$1 AND state='held'", "SELECT COUNT(*) FROM gpu_leases WHERE training_job_id=? AND state='held'", g.job.ID)).Scan(&held); e != nil || held != 1 {
			t.Fatal("cancellation released physical owner", e)
		}
		same, e := g.cancel(v.Revision)
		if e != nil || !reflect.DeepEqual(v, same) {
			t.Fatal(e)
		}
		g.exec(t, "UPDATE training_jobs SET state='recovery_required' WHERE id=$1", "UPDATE training_jobs SET state='recovery_required' WHERE id=?", g.job.ID)
		failed, e := g.cancel(v.Revision)
		trainingCreatorWant(t, failed, e, "TRAINING_PHYSICAL_STOP_REQUIRED")
		g.audits(t, 1)
	})
	t.Run("atomic_cancellation_audit_rollback_and_unknown_recovery", func(t *testing.T) {
		g := trainingOperatorSetup(t)
		g.exec(t, `CREATE FUNCTION explicit_operator_audit_failure() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'explicit cancellation audit test'; END;$$`, `CREATE TRIGGER explicit_operator_audit_failure BEFORE INSERT ON audit_events FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='explicit cancellation audit test'`)
		if g.f.Store.Dialect() == transaction.PostgreSQL {
			g.exec(t, `CREATE TRIGGER explicit_operator_audit_failure BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION explicit_operator_audit_failure()`, "")
		}
		v, e := g.cancel(g.job.Revision)
		trainingCreatorWant(t, v, e, "STORAGE_UNAVAILABLE")
		g.audits(t, 0)
		current, e := g.s.Get(g.ctx, g.a, g.f.Workspace, g.job.ID)
		if e != nil || current.State != "queued" || current.Revision != g.job.Revision {
			t.Fatal("audit rollback lost intent", e)
		}
		g.exec(t, "DROP TRIGGER explicit_operator_audit_failure ON audit_events", "DROP TRIGGER explicit_operator_audit_failure")
		x := trainingOperatorWrapped(g, &trainingCreatorStore{Store: g.f.Store, at: 1, unknown: true})
		v, e = x.cancel(g.job.Revision)
		trainingCreatorWant(t, v, e, string(transaction.CommitUnknown))
		current, e = g.s.Get(g.ctx, g.a, g.f.Workspace, g.job.ID)
		if e != nil || current.State != "cancelled" {
			t.Fatal("read durable reduction after unknown commit", e)
		}
		same, e := g.cancel(current.Revision)
		if e != nil || !reflect.DeepEqual(same, current) {
			t.Fatal(e)
		}
		g.audits(t, 1)
	})
	t.Run("history_UTC_keysets_and_cursor_scope", func(t *testing.T) {
		g := trainingOperatorSetup(t)
		g.histories(t)
		query := url.Values{"limit": {"1"}}
		seen := map[string]bool{}
		cursor := ""
		for range 3 {
			p, e := g.s.Attempts(g.ctx, g.a, g.f.Workspace, g.job.ID, query)
			if e != nil || len(p.Items) != 1 || seen[p.Items[0].ID] || p.Items[0].CreatedAt.Location() != time.UTC || p.Items[0].FinishedAt == nil || p.Items[0].FinishedAt.Location() != time.UTC {
				t.Fatal("actual UTC history keyset", p, e)
			}
			seen[p.Items[0].ID] = true
			if p.NextCursor != nil {
				query.Set("cursor", *p.NextCursor)
				cursor = *p.NextCursor
			}
		}
		c, e := g.s.Checkpoints(g.ctx, g.a, g.f.Workspace, g.job.ID, url.Values{})
		if e != nil || len(c.Items) != 3 || c.Items[0].CreatedAt.Location() != time.UTC {
			t.Fatal("retained checkpoint metadata", e)
		}
		r, e := g.s.Recoveries(g.ctx, g.a, g.f.Workspace, g.job.ID, url.Values{})
		if e != nil || len(r.Items) != 3 || r.Items[0].CreatedAt.Location() != time.UTC {
			t.Fatal("retained recovery history", e)
		}
		c, e = g.s.Checkpoints(g.ctx, g.a, g.f.Workspace, g.job.ID, url.Values{"cursor": {cursor}})
		if e == nil || e.Error() != "INVALID_CURSOR" || len(c.Items) != 0 {
			t.Fatal("cross kind cursor", e)
		}
		p, e := g.s.Attempts(g.ctx, g.a, uuid.NewString(), g.job.ID, url.Values{})
		if e == nil || e.Error() != "RESOURCE_NOT_FOUND" || len(p.Items) != 0 {
			t.Fatal("foreign history", e)
		}
	})
	t.Run("actual_metric_payload_sequence_and_failed_empty_page", func(t *testing.T) {
		g := trainingOperatorSetup(t)
		attempt := g.attempt(t, 1)
		for i := 1; i <= 3; i++ {
			g.metric(t, attempt, i, `{"values":{"loss":1}}`)
		}
		query := url.Values{"limit": {"1"}}
		for want := int64(1); want <= 3; want++ {
			p, e := g.s.Metrics(g.ctx, g.a, g.f.Workspace, g.job.ID, attempt, query)
			if e != nil || len(p.Items) != 1 || p.Items[0].Sequence != want || p.Items[0].RecordedAt.Location() != time.UTC {
				t.Fatal("actual metric order", p, e)
			}
			if p.NextCursor != nil {
				query.Set("cursor", *p.NextCursor)
			}
		}
		p, e := g.s.Metrics(g.ctx, g.a, g.f.Workspace, g.job.ID, uuid.NewString(), url.Values{})
		if e == nil || e.Error() != "TRAINING_NOT_FOUND" || len(p.Items) != 0 {
			t.Fatal("foreign attempt metrics", e)
		}
		g.metric(t, attempt, 4, `{"values":{"unexpected":1}}`)
		p, e = g.s.Metrics(g.ctx, g.a, g.f.Workspace, g.job.ID, attempt, url.Values{})
		if e == nil || e.Error() != "TRAINING_DATA_INVALID" || len(p.Items) != 0 || p.NextCursor != nil {
			t.Fatal("malformed late metric leaked partial rows", e)
		}
	})
	t.Run("actual_scoped_fresh_stale_invalid_target_and_capabilities", func(t *testing.T) {
		g := trainingOperatorSetup(t)
		g.openStore(t)
		g.hardware(t)
		v, e := g.s.Target(g.ctx, g.a, g.f.Workspace, g.input.NodeID)
		if e != nil || v.NodeID != g.input.NodeID || len(v.GPUs) != 1 || v.GPUs[0].UUID != g.input.GPUUUID || v.ObservedAt.Location() != time.UTC {
			t.Fatal("actual scoped observation", v, e)
		}
		caps, e := g.s.Capabilities(g.ctx, g.a, g.f.Workspace)
		if e != nil || !caps.Enabled || !caps.CanCreate {
			t.Fatal("actual store/config availability", e)
		}
		viewer := g.portal(t, "viewer")
		caps, e = g.s.Capabilities(g.ctx, viewer, g.f.Workspace)
		if e != nil || !caps.Enabled || caps.CanCreate {
			t.Fatal("current viewer controls", e)
		}
		g.exec(t, "UPDATE inference_nodes SET received_at=clock_timestamp()-interval '1 day' WHERE node_id=$1", "UPDATE inference_nodes SET received_at=TIMESTAMPADD(DAY,-1,UTC_TIMESTAMP(6)) WHERE node_id=?", g.input.NodeID)
		v, e = g.s.Target(g.ctx, g.a, g.f.Workspace, g.input.NodeID)
		if e == nil || e.Error() != "TRAINING_PREFLIGHT_REQUIRED" || !reflect.DeepEqual(v, training.Target{}) {
			t.Fatal("stale observation accepted", e)
		}
		g.exec(t, "UPDATE inference_nodes SET observation='{}',received_at=clock_timestamp() WHERE node_id=$1", "UPDATE inference_nodes SET observation=CAST('{}' AS JSON),received_at=UTC_TIMESTAMP(6) WHERE node_id=?", g.input.NodeID)
		v, e = g.s.Target(g.ctx, g.a, g.f.Workspace, g.input.NodeID)
		if e == nil || e.Error() != "TRAINING_DATA_INVALID" || !reflect.DeepEqual(v, training.Target{}) {
			t.Fatal("malformed observation leaked fields", e)
		}
		v, e = g.s.Target(g.ctx, g.a, uuid.NewString(), g.input.NodeID)
		if e == nil || e.Error() != "RESOURCE_NOT_FOUND" || !reflect.DeepEqual(v, training.Target{}) {
			t.Fatal("foreign target", e)
		}
	})
	t.Run("cancel_deadline_query_coherence_and_uncertain_zero_reads", func(t *testing.T) {
		g := trainingOperatorSetup(t)
		g.histories(t)
		other := *g.s
		ws := *g.s.Workspaces
		ws.Identity = &identity.Service{Storage: g.f.Store}
		other.Workspaces = &ws
		denied, err := other.Cancel(g.ctx, g.a, g.f.Workspace, g.job.ID, training.ActionRequest{ExpectedRevision: g.job.Revision, Reason: "Mismatched owning identity"}, uuid.NewString())
		trainingCreatorWant(t, denied, err, "TRAINING_IDENTITY_REQUIRED")
		other = *g.s
		inf := *g.s.Inference
		nodeCopy := *g.s.Nodes
		inf.Nodes = &nodeCopy
		other.Inference = &inf
		target, err := other.Target(g.ctx, g.a, g.f.Workspace, g.input.NodeID)
		if err == nil || err.Error() != "TRAINING_STORAGE_MISMATCH" || !reflect.DeepEqual(target, training.Target{}) {
			t.Fatal("mismatched target composition", err)
		}
		ctx, cancel := context.WithCancel(g.ctx)
		cancel()
		v, e := g.s.Cancel(ctx, g.a, g.f.Workspace, g.job.ID, training.ActionRequest{ExpectedRevision: g.job.Revision, Reason: "Cancelled context"}, uuid.NewString())
		trainingCreatorWant(t, v, e, string(transaction.Cancelled))
		ctx, cancel = context.WithDeadline(g.ctx, time.Now().Add(-time.Second))
		defer cancel()
		v, e = g.s.Cancel(ctx, g.a, g.f.Workspace, g.job.ID, training.ActionRequest{ExpectedRevision: g.job.Revision, Reason: "Expired context"}, uuid.NewString())
		trainingCreatorWant(t, v, e, string(transaction.Timeout))
		x := trainingOperatorWrapped(g, &trainingCreatorStore{Store: g.f.Store, at: 1, unknown: true})
		p, e := x.s.Attempts(x.ctx, x.a, x.f.Workspace, x.job.ID, url.Values{})
		if e == nil || e.Error() != string(transaction.CommitUnknown) || len(p.Items) != 0 || p.NextCursor != nil {
			t.Fatal("uncertain history leaked rows", e)
		}
		x = trainingOperatorWrapped(g, &trainingCreatorStore{Store: g.f.Store, at: 1, unknown: true})
		caps, e := x.s.Capabilities(x.ctx, x.a, x.f.Workspace)
		if e == nil || e.Error() != string(transaction.CommitUnknown) || !reflect.DeepEqual(caps, training.Capabilities{}) {
			t.Fatal("uncertain capabilities leaked values", e)
		}
		g.exec(t, "ALTER TABLE training_attempts RENAME TO explicit_operator_missing_attempts", "RENAME TABLE training_attempts TO explicit_operator_missing_attempts")
		p, e = g.s.Attempts(g.ctx, g.a, g.f.Workspace, g.job.ID, url.Values{})
		if e == nil || e.Error() != "STORAGE_UNAVAILABLE" || len(p.Items) != 0 {
			t.Fatal("query failure leaked history", e)
		}
		g.exec(t, "ALTER TABLE explicit_operator_missing_attempts RENAME TO training_attempts", "RENAME TABLE explicit_operator_missing_attempts TO training_attempts")
		g.audits(t, 0)
	})
}
