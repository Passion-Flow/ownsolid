package components_test

import (
	"context"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/components"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/google/uuid"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type scenario struct {
	service       *components.Service
	admin, portal identity.Auth
	enrolled      wire.Enrolled
	handshake     wire.Handshake
}

func setup(t *testing.T) scenario    { return setupComposed(t) }
func setupM04(t *testing.T) scenario { return setupComposed(t) }
func setupComposed(t *testing.T) scenario {
	t.Helper()
	s := fixture.New(t)
	p := fixture.Bootstrap(t, s)
	a := fixture.Login(t, s, "admin@example.invalid", p, "admin")
	portal := fixture.Login(t, s, "admin@example.invalid", p, "portal")
	composed := identityhttp.New(s)
	n, c := composed.Nodes.Service, composed.Components.Service
	join, e := n.MintForProfile(context.Background(), a, a.Me.Workspace.ID, "Synthetic management node", wire.SystemdComponentsProfile, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	h := wire.Handshake{ProtocolVersion: wire.ManagementVersion, ExecutionProfile: wire.SystemdComponentsProfile, Capabilities: wire.Capabilities(wire.SystemdComponentsProfile), Facts: wire.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "synthetic-component-node", LogicalCPUs: 1, MemoryTotalBytes: 1024, DiskPath: "/", DiskTotalBytes: 4096, DiskAvailableBytes: 2048, ObservedAt: time.Now().UTC(), AgentVersion: wire.AgentVersion}}
	enrolled, e := n.Enroll(context.Background(), wire.Enrollment{JoinToken: join.Token, Handshake: h}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	return scenario{c, a, portal, enrolled, h}
}
func expect(t *testing.T, e error, want string) {
	t.Helper()
	if e == nil || e.Error() != want {
		t.Fatalf("expected %s observed %v", want, e)
	}
}
func TestM04DurableCommandsIdempotencyScopeAndLeaseFencing(t *testing.T) {
	f := setupM04(t)
	s, n := f.service, f.service.Nodes
	ctx := context.Background()
	id := f.enrolled.NodeID
	key := uuid.NewString()
	task, e := s.Request(ctx, f.admin, id, "start", key, "Synthetic lifecycle", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	again, e := s.Request(ctx, f.admin, id, "start", key, "Synthetic retry", uuid.NewString())
	if e != nil || again.ID != task.ID {
		t.Fatal("idempotency not persistent", e)
	}
	_, e = s.Request(ctx, f.admin, id, "stop", key, "Synthetic conflicting reuse", uuid.NewString())
	expect(t, e, "IDEMPOTENCY_CONFLICT")
	_, e = s.Request(ctx, f.portal, id, "stop", uuid.NewString(), "Denied", uuid.NewString())
	expect(t, e, "ACCESS_DENIED")
	_, e = s.Get(ctx, f.portal, uuid.NewString(), id)
	expect(t, e, "RESOURCE_NOT_FOUND")
	_, e = s.Request(ctx, f.admin, id, "stop", uuid.NewString(), "Synthetic busy", uuid.NewString())
	expect(t, e, "NODE_BUSY")
	claim, e := n.Claim(ctx, id, f.enrolled.Credential, uuid.NewString())
	if e != nil || claim.Lease == nil || claim.Lease.Component == nil || claim.Lease.Component.Action != "start" {
		t.Fatal("typed command not claimed", e)
	}
	lease := claim.Lease
	if _, e = n.Renew(ctx, id, f.enrolled.Credential, task.ID, lease.LeaseToken); e != nil {
		t.Fatal(e)
	}
	_, e = n.Renew(ctx, id, f.enrolled.Credential, task.ID, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	expect(t, e, "AGENT_LEASE_STALE")
	observation := wire.ComponentObservation{Component: wire.NodeExporter, Version: wire.NodeExporterVersion, State: "unknown", ObservedAt: time.Now().UTC(), ErrorCode: "COMPONENT_SYSTEMD_UNAVAILABLE"}
	result := wire.Result{LeaseToken: lease.LeaseToken, Outcome: "failure", ErrorCode: observation.ErrorCode, Component: &observation}
	state, e := n.Complete(ctx, id, f.enrolled.Credential, task.ID, result, uuid.NewString())
	if e != nil || state != "failed" {
		t.Fatal("failure not durably recorded", e)
	}
	if _, e = n.Complete(ctx, id, f.enrolled.Credential, task.ID, result, uuid.NewString()); e != nil {
		t.Fatal("lost acknowledgment not reconciled", e)
	}
	_, e = n.Renew(ctx, id, f.enrolled.Credential, task.ID, lease.LeaseToken)
	expect(t, e, "AGENT_LEASE_STALE")
	view, e := s.Get(ctx, f.portal, f.portal.Me.Workspace.ID, id)
	if e != nil || view.Observation == nil || view.Observation.ErrorCode != observation.ErrorCode || view.Stale {
		t.Fatal("actual failure or receipt freshness missing", e)
	}
	if _, e = n.DB.Exec(ctx, "UPDATE managed_nodes SET last_heartbeat_at=clock_timestamp()-interval '121 seconds' WHERE id=$1", id); e != nil {
		t.Fatal(e)
	}
	view, e = s.Get(ctx, f.portal, f.portal.Me.Workspace.ID, id)
	if e != nil || !view.Stale || view.Observation.State != "unknown" {
		t.Fatal("disconnect fabricated state", e)
	}
}
func TestM04ComponentValidationCancellationAndAuditRollback(t *testing.T) {
	f := setupM04(t)
	s, n := f.service, f.service.Nodes
	ctx := context.Background()
	id := f.enrolled.NodeID
	if _, e := n.DB.Exec(ctx, `CREATE FUNCTION reject_component_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='component.start_requested' THEN RAISE EXCEPTION 'synthetic audit failure';END IF;RETURN NEW;END $$;CREATE TRIGGER reject_component BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_component_audit()`); e != nil {
		t.Fatal(e)
	}
	_, e := s.Request(ctx, f.admin, id, "start", uuid.NewString(), "Synthetic rollback", uuid.NewString())
	if e == nil {
		t.Fatal("audit failure ignored")
	}
	var count int
	if e = n.DB.QueryRow(ctx, "SELECT count(*) FROM node_tasks WHERE node_id=$1", id).Scan(&count); e != nil || count != 0 {
		t.Fatal("task survived rollback", e)
	}
	if _, e = n.DB.Exec(ctx, "DROP TRIGGER reject_component ON audit_events"); e != nil {
		t.Fatal(e)
	}
	task, e := s.Request(ctx, f.admin, id, "start", uuid.NewString(), "Synthetic cancellation", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	claim, e := n.Claim(ctx, id, f.enrolled.Credential, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	observation := wire.ComponentObservation{Component: wire.NodeExporter, Version: wire.NodeExporterVersion, State: "stopped", Managed: true, ObservedAt: time.Now().UTC()}
	_, e = n.Complete(ctx, id, f.enrolled.Credential, task.ID, wire.Result{LeaseToken: claim.Lease.LeaseToken, Outcome: "success", Component: &observation}, uuid.NewString())
	expect(t, e, "VALIDATION_FAILED")
	node, e := n.Get(ctx, f.admin, "", id)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = n.Update(ctx, f.admin, id, node.Name, "disabled", node.Version, "Synthetic disable", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	_, e = n.Renew(ctx, id, f.enrolled.Credential, task.ID, claim.Lease.LeaseToken)
	expect(t, e, "ACCESS_DENIED")
	var status string
	if e = n.DB.QueryRow(ctx, "SELECT state FROM node_tasks WHERE id=$1", task.ID).Scan(&status); e != nil || status != "cancelled" {
		t.Fatal("disabled node retained task", e)
	}
}
func TestM04OfflineArtifactIntegrityAndReadOnlyEnrollment(t *testing.T) {
	f := setupM04(t)
	s, n := f.service, f.service.Nodes
	ctx := context.Background()
	s.ArchiveFile = filepath.Join(t.TempDir(), "corrupt.tar.gz")
	if e := os.WriteFile(s.ArchiveFile, []byte("synthetic corrupt archive"), 0600); e != nil {
		t.Fatal(e)
	}
	_, e := s.Request(ctx, f.admin, f.enrolled.NodeID, "install", uuid.NewString(), "Synthetic corrupt install", uuid.NewString())
	expect(t, e, "COMPONENT_ASSET_INVALID")
	join, e := n.Mint(ctx, f.admin, f.admin.Me.Workspace.ID, "Legacy read-only node", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	h := f.handshake
	h.ProtocolVersion = wire.Version
	h.ExecutionProfile = ""
	h.Capabilities = wire.Capabilities(wire.ReadOnlyProfile)
	enrolled, e := n.Enroll(ctx, wire.Enrollment{JoinToken: join.Token, Handshake: h}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.Request(ctx, f.admin, enrolled.NodeID, "start", uuid.NewString(), "Denied capability", uuid.NewString())
	expect(t, e, "COMPONENT_PROFILE_UNAVAILABLE")
	if _, e = n.CreateTask(ctx, f.admin, enrolled.NodeID, wire.CollectFacts, uuid.NewString(), uuid.NewString()); e != nil {
		t.Fatal("legacy discovery broken", e)
	}
}

func TestM04RenewalCannotExtendTheTaskDeadline(t *testing.T) {
	f := setupM04(t)
	s, n := f.service, f.service.Nodes
	ctx := context.Background()
	id := f.enrolled.NodeID
	task, e := s.Request(ctx, f.admin, id, "start", uuid.NewString(), "Synthetic deadline fence", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	claim, e := n.Claim(ctx, id, f.enrolled.Credential, uuid.NewString())
	if e != nil || claim.Lease == nil {
		t.Fatal(e)
	}
	if _, e = n.DB.Exec(ctx, "UPDATE node_tasks SET deadline_at=clock_timestamp()+interval '200 milliseconds' WHERE id=$1", task.ID); e != nil {
		t.Fatal(e)
	}
	lease, e := n.Renew(ctx, id, f.enrolled.Credential, task.ID, claim.Lease.LeaseToken)
	if e != nil || lease.ValidForMilliseconds < 1 || lease.ValidForMilliseconds > 200 {
		t.Fatal("renewal outlived its deadline", e)
	}
	time.Sleep(250 * time.Millisecond)
	_, e = n.Renew(ctx, id, f.enrolled.Credential, task.ID, claim.Lease.LeaseToken)
	expect(t, e, "AGENT_LEASE_STALE")
}

// Structured observations here are explicit service fixtures, not installed-host evidence.
func TestM04MinimalLifecycleCurrentObservationsAndResultAuditRollback(t *testing.T) {
	f := setupM04(t)
	s, n := f.service, f.service.Nodes
	ctx := t.Context()
	id := f.enrolled.NodeID
	s.ArchiveFile = filepath.Join(os.Getenv("PROJECT_ONE_STATE_ROOT"), "dependencies/components/node-exporter/1.12.1/linux-amd64/node_exporter-1.12.1.linux-amd64.tar.gz")
	for _, action := range []string{"install", "start", "stop", "observe", "remove"} {
		task, e := s.Request(ctx, f.admin, id, action, uuid.NewString(), "Synthetic current lifecycle", uuid.NewString())
		if e != nil {
			t.Fatal(action, e)
		}
		claim, e := n.Claim(ctx, id, f.enrolled.Credential, uuid.NewString())
		if e != nil || claim.Lease == nil {
			t.Fatal(e)
		}
		o := wire.ComponentObservation{Component: wire.NodeExporter, Version: wire.NodeExporterVersion, State: "running", Managed: true, ObservedAt: time.Now().UTC(), Metrics: &wire.ComponentMetrics{CPUSecondsTotal: 5, CPUIdleSecondsTotal: 2, MemoryTotalBytes: 1024, MemoryAvailableBytes: 512, FilesystemTotalBytes: 2048, FilesystemAvailableBytes: 1024}}
		if action == "stop" || action == "observe" {
			o.State = "stopped"
			o.Metrics = nil
		}
		if action == "remove" {
			o.State = "not_installed"
			o.Managed = false
			o.Metrics = nil
		}
		input := wire.Result{LeaseToken: claim.Lease.LeaseToken, Outcome: "success", Component: &o}
		if action == "install" {
			if _, e = n.DB.Exec(ctx, `CREATE FUNCTION reject_result_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='node.task_succeeded' THEN RAISE EXCEPTION 'synthetic result audit failure';END IF;RETURN NEW;END $$;CREATE TRIGGER reject_result BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_result_audit()`); e != nil {
				t.Fatal(e)
			}
			if _, e = n.Complete(ctx, id, f.enrolled.Credential, task.ID, input, uuid.NewString()); e == nil {
				t.Fatal("result audit failure accepted")
			}
			var state string
			if e = n.DB.QueryRow(ctx, "SELECT state FROM node_tasks WHERE id=$1", task.ID).Scan(&state); e != nil || state != "running" {
				t.Fatal("result transaction escaped", e)
			}
			view, e := s.Get(ctx, f.admin, "", id)
			if e != nil || view.Observation != nil {
				t.Fatal("observation escaped failed audit", e)
			}
			if _, e = n.DB.Exec(ctx, "DROP TRIGGER reject_result ON audit_events"); e != nil {
				t.Fatal(e)
			}
		}
		if state, e := n.Complete(ctx, id, f.enrolled.Credential, task.ID, input, uuid.NewString()); e != nil || state != "succeeded" {
			t.Fatal(action, state, e)
		}
		if _, e = n.Complete(ctx, id, f.enrolled.Credential, task.ID, input, uuid.NewString()); e != nil {
			t.Fatal("exact result replay", e)
		}
		view, e := s.Get(ctx, f.portal, f.portal.Me.Workspace.ID, id)
		if e != nil || view.Observation == nil || view.Observation.State != o.State || view.Stale || view.LastTaskID == nil || *view.LastTaskID != task.ID {
			t.Fatal("current observation", action, e)
		}
	}
	var later int
	if e := n.DB.QueryRow(ctx, "SELECT count(*) FROM pg_tables WHERE schemaname=current_schema() AND tablename='component_log_snapshots'").Scan(&later); e != nil || later != 0 {
		t.Fatal("later schema activated", e)
	}
}

// Heartbeats that repeat a state reach the row in batches, with their latest metrics, and an
// older report never overwrites a newer synchronous write (ADR 0022).
func TestUnchangedObservationsReachTheRowInBatches(t *testing.T) {
	f := setupComposed(t)
	ctx := context.Background()
	n := f.service.Nodes
	if e := n.EnableLiveness(ctx); e != nil {
		t.Fatal(e)
	}
	id := f.enrolled.NodeID
	beat := func(state string, cpu float64) wire.Handshake {
		h := f.handshake
		o := wire.ComponentObservation{Component: wire.NodeExporter, Version: wire.NodeExporterVersion, State: state, Managed: true, ObservedAt: time.Now().UTC()}
		if state == "running" {
			o.Metrics = &wire.ComponentMetrics{CPUSecondsTotal: cpu, CPUIdleSecondsTotal: 1, MemoryTotalBytes: 1, MemoryAvailableBytes: 1, FilesystemTotalBytes: 1, FilesystemAvailableBytes: 1}
		}
		h.Component = &o
		if _, e := n.Heartbeat(ctx, id, f.enrolled.Credential, h); e != nil {
			t.Fatal(e)
		}
		return h
	}
	read := func() (xmin, state string, cpu float64, observed, verified time.Time) {
		t.Helper()
		if e := n.DB.QueryRow(ctx, `SELECT xmin::text,observed_state,COALESCE((observation->'metrics'->>'cpu_seconds_total')::float8,0),observed_at,last_verified_at FROM managed_components WHERE node_id=$1`, id).Scan(&xmin, &state, &cpu, &observed, &verified); e != nil {
			t.Fatal(e)
		}
		return
	}
	beat("running", 1)
	xmin, _, cpu, observed, verified := read()
	if cpu != 1 {
		t.Fatal(cpu)
	}
	beat("running", 2)
	if again, _, _, _, _ := read(); again != xmin {
		t.Fatal("an unchanged observation wrote the row")
	}
	if e := n.FlushLiveness(ctx); e != nil {
		t.Fatal(e)
	}
	_, state, cpu, flushedAt, flushedVerified := read()
	if state != "running" || cpu != 2 || !flushedAt.After(observed) || !flushedVerified.After(verified) {
		t.Fatal(state, cpu, flushedAt, flushedVerified)
	}

	beat("running", 3)
	beat("stopped", 0)
	if e := n.FlushLiveness(ctx); e != nil {
		t.Fatal(e)
	}
	if _, state, cpu, _, _ = read(); state != "stopped" || cpu != 0 {
		t.Fatalf("an older report overwrote a newer write: %s %v", state, cpu)
	}
}
